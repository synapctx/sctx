// Package discover replays Claude Code session transcripts through the hook's
// rewrite decision to find what sctx did NOT wrap although a rule would have,
// and which programs have no formatter at all. It is read-only and local: it
// reads transcripts and nothing leaves the machine.
//
// Two facts shape it. A transcript records a command as the MODEL wrote it,
// before any hook rewrote it, so a hooked command looks bare; callers pass
// Wrapped (per session and command key, from the local stats store) so those
// are discounted rather than reported as misses. And output sizes come from
// the tool_result as the agent saw it, so a wrapped run's result is already
// compressed; that is another reason to discount rather than trust it.
package discover

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind is how the rewrite engine classified a command.
type Kind int

const (
	KindSkip Kind = iota // not wrapped, not a gap
	KindWrap             // a rule applies
	KindGap              // wrappable but no formatter
)

// Classifier replays one command through the hook's decision. Key is the
// progkey program key (never arguments).
type Classifier func(cmd string) (kind Kind, key string)

// maxLine bounds one transcript line held in memory; longer lines are skipped
// (a multi-megabyte tool result is not worth its memory).
const maxLine = 64 << 20

// Options configures a scan.
type Options struct {
	Dirs     []string  // transcript directories (each holds *.jsonl)
	Since    time.Time // zero = all time
	Classify Classifier
	// Wrapped is the number of runs sctx recorded per (session id, command
	// key); that many would-be-wrapped commands per key are treated as wrapped
	// by the hook already.
	Wrapped map[string]map[string]int64
	TopN    int // rows per table; 0 = 10
}

// Row aggregates commands under one program key.
type Row struct {
	Key         string `json:"key"`
	Count       int64  `json:"count"`
	OutputBytes int64  `json:"outputBytes"`
	EstTokens   int64  `json:"estTokens"`
	// SavedPct and EstSavedTokens are set on missed rows only.
	SavedPct       float64 `json:"savedPct,omitempty"`
	EstSavedTokens int64   `json:"estSavedTokens,omitempty"`
	// Estimated is true when SavedPct is not this key's own measured history.
	Estimated bool `json:"estimated,omitempty"`
}

// Report is the result of a scan.
type Report struct {
	SchemaVersion  int   `json:"schemaVersion"`
	Files          int   `json:"files"`
	Sessions       int   `json:"sessions"`
	BashCommands   int64 `json:"bashCommands"`
	AlreadyWrapped int64 `json:"alreadyWrapped"`
	Missed         []Row `json:"missed"`
	MissedCount    int64 `json:"missedCount"`
	MissedTokens   int64 `json:"missedEstTokens"`
	MissedSaved    int64 `json:"missedEstSavedTokens"`
	Uncovered      []Row `json:"uncovered"`
	UncoveredCount int64 `json:"uncoveredCount"`
	SkippedLines   int64 `json:"skippedLines"`
}

type agg struct{ count, bytes int64 }

type pending struct {
	session string
	kind    Kind
	key     string
}

// Scan reads every transcript in opts.Dirs and aggregates.
func Scan(ctx context.Context, opts Options) (Report, error) {
	topN := opts.TopN
	if topN <= 0 {
		topN = 10
	}
	// Mutable copy: each hook-wrapped run is spent once.
	budget := map[string]map[string]int64{}
	for s, m := range opts.Wrapped {
		c := make(map[string]int64, len(m))
		for k, v := range m {
			c[k] = v
		}
		budget[s] = c
	}

	rep := Report{SchemaVersion: 1}
	missed := map[string]*agg{}
	uncovered := map[string]*agg{}
	sessions := map[string]bool{}

	tally := func(p pending, size int64) {
		rep.BashCommands++
		switch p.kind {
		case KindWrap:
			if b := budget[p.session][p.key]; b > 0 {
				budget[p.session][p.key] = b - 1
				rep.AlreadyWrapped++
				return
			}
			a := missed[p.key]
			if a == nil {
				a = &agg{}
				missed[p.key] = a
			}
			a.count++
			a.bytes += size
		case KindGap:
			a := uncovered[p.key]
			if a == nil {
				a = &agg{}
				uncovered[p.key] = a
			}
			a.count++
			a.bytes += size
		}
	}

	for _, dir := range opts.Dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a project dir that vanished is not an error
		}
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return Report{}, err
			}
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if info, err := e.Info(); err == nil && !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
				continue
			}
			rep.Files++
			if err := scanFile(ctx, path, opts, sessions, tally, &rep); err != nil {
				return Report{}, fmt.Errorf("scanning %s: %w", filepath.Base(path), err)
			}
		}
	}
	rep.Sessions = len(sessions)

	rep.Missed, rep.MissedCount, rep.MissedTokens = rows(missed, 0)
	rep.Uncovered, rep.UncoveredCount, _ = rows(uncovered, topN)
	return rep, nil
}

// ApplySavings fills SavedPct/EstSavedTokens on missed rows (and the report
// total) from per-key history; keys without history use fallback, flagged
// Estimated. Scan keeps every missed row so the totals cover all of them; call
// Trim afterwards to shorten the table.
func (r *Report) ApplySavings(history map[string]float64, fallback float64) {
	r.MissedSaved = 0
	for i := range r.Missed {
		row := &r.Missed[i]
		pct, ok := history[row.Key]
		if !ok {
			pct, row.Estimated = fallback, true
		}
		row.SavedPct = pct
		row.EstSavedTokens = int64(float64(row.EstTokens) * pct)
		r.MissedSaved += row.EstSavedTokens
	}
}

// Trim keeps the n heaviest missed rows (totals are unaffected).
func (r *Report) Trim(n int) {
	if n > 0 && len(r.Missed) > n {
		r.Missed = r.Missed[:n]
	}
}

// rows turns an aggregate map into rows ordered by output volume then count.
func rows(m map[string]*agg, limit int) ([]Row, int64, int64) {
	out := make([]Row, 0, len(m))
	var count, toks int64
	for k, a := range m {
		out = append(out, Row{Key: k, Count: a.count, OutputBytes: a.bytes, EstTokens: a.bytes / 4})
		count += a.count
		toks += a.bytes / 4
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OutputBytes != out[j].OutputBytes {
			return out[i].OutputBytes > out[j].OutputBytes
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, count, toks
}

type block struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input struct {
		Command string `json:"command"`
	} `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
	Content   jsontext.Value `json:"content"`
}

type record struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	Message   struct {
		Content jsontext.Value `json:"content"`
	} `json:"message"`
}

var (
	toolUseMark    = []byte(`"tool_use"`)
	toolResultMark = []byte(`"tool_result"`)
)

func scanFile(ctx context.Context, path string, opts Options, sessions map[string]bool,
	tally func(pending, int64), rep *Report) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	open := map[string]pending{} // tool_use id -> command awaiting its result
	seen := map[string]bool{}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, tooLong, err := readLine(r)
		if len(line) > 0 || tooLong {
			if tooLong {
				rep.SkippedLines++
			} else {
				handleLine(line, opts, sessions, open, seen, tally, rep)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	// Commands whose result never arrived still ran; their volume is unknown.
	for _, p := range open {
		tally(p, 0)
	}
	return nil
}

func handleLine(line []byte, opts Options, sessions map[string]bool, open map[string]pending,
	seen map[string]bool, tally func(pending, int64), rep *Report) {
	hasUse := bytes.Contains(line, toolUseMark)
	hasRes := bytes.Contains(line, toolResultMark)
	if !hasUse && !hasRes {
		return
	}
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		rep.SkippedLines++
		return
	}
	// api-request-blob and similar duplicate the same tool calls.
	if rec.Type != "assistant" && rec.Type != "user" {
		return
	}
	if !opts.Since.IsZero() && rec.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil && ts.Before(opts.Since) {
			return
		}
	}
	if len(rec.Message.Content) == 0 || rec.Message.Content[0] != '[' {
		return
	}
	var blocks []block
	if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
		rep.SkippedLines++
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			if b.Name != "Bash" || b.Input.Command == "" || b.ID == "" || seen[b.ID] {
				continue
			}
			seen[b.ID] = true
			sessions[rec.SessionID] = true
			kind, key := KindSkip, ""
			if opts.Classify != nil {
				kind, key = opts.Classify(b.Input.Command)
			}
			open[b.ID] = pending{session: rec.SessionID, kind: kind, key: key}
		case "tool_result":
			p, ok := open[b.ToolUseID]
			if !ok {
				continue
			}
			delete(open, b.ToolUseID)
			tally(p, resultSize(b.Content))
		}
	}
}

// resultSize is the byte length of a tool_result's text as the agent saw it.
func resultSize(v jsontext.Value) int64 {
	if len(v) == 0 {
		return 0
	}
	switch v[0] {
	case '"':
		var s string
		if json.Unmarshal(v, &s) == nil {
			return int64(len(s))
		}
	case '[':
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(v, &parts) == nil {
			var n int64
			for _, p := range parts {
				n += int64(len(p.Text))
			}
			return n
		}
	}
	return 0
}

// readLine returns the next line without its newline. A line longer than
// maxLine is consumed and reported as tooLong with no content, so one huge
// tool result cannot stop the scan or exhaust memory.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, e := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > maxLine {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), tooLong, e
	}
}
