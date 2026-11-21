package gh

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// `gh run view --log` / `--log-failed` print one line per log line as
// `job<TAB>step<TAB>[BOM]timestamp text`. The job and step repeat on every
// line, every line carries a 28-byte timestamp, and the runner's own setup
// chatter and `##[group]` blocks (package installs, checkout, tool caches)
// dominate the bytes. The renderer prints each job and step once, drops
// timestamps and runner setup, collapses a group with no error-looking line to
// a `▸ title (+N lines)` marker, and keeps error lines, annotations
// (##[error]/##[warning]/##[notice]) and the head and tail of anything long.
var (
	logTimestampRE = regexp.MustCompile(`^\x{FEFF}?\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z ?`)
	logAnsiRE      = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	logErrRE       = regexp.MustCompile(`(?i)(##\[(error|warning|notice)\]|\berror\b|\berror:|\bfail(ed|ure|ures|s)?\b|\bpanic\b|\bexception\b|traceback|\bfatal\b|\bwarning\b|exit code [1-9]|--- FAIL|\bFAIL\b)`)
	logSetupRE     = regexp.MustCompile(`^(Current runner version:|Secret source:|Cache mode:|Prepare workflow directory|Prepare all required actions|Getting action download info|Download action repository|Download immutable action package|Complete job name:|Waiting for a runner|Requested labels:|Job defined at:|Waiting for a hosted runner|Evaluate and set job outputs|Set output |Cleaning up orphan processes|Post job cleanup\.)`)
	logSetupGroups = map[string]bool{
		"Runner Image Provisioner": true, "Operating System": true, "Runner Image": true,
		"VM Image": true, "GITHUB_TOKEN Permissions": true, "Virtual Environment": true,
	}
)

const (
	logPlainCap = 60 // ungrouped lines kept verbatim before head/tail/error condensing
	logGroupCap = 30
)

type logLine struct{ job, step, text string }

func aggressiveRunLog(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	parsed := make([]logLine, 0, len(lines))
	for _, l := range lines {
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) != 3 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		text := logAnsiRE.ReplaceAllString(logTimestampRE.ReplaceAllString(parts[2], ""), "")
		parsed = append(parsed, logLine{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimRight(text, " \t")})
	}
	return renderRunLog(parsed, len(raw))
}

type logRenderer struct {
	out                           []string
	plain                         []string
	group                         *logGroup
	setup, blank, collapsedGroups int
}

type logGroup struct {
	title string
	body  []string
	hasEr bool
}

func renderRunLog(lines []logLine, rawLen int) (format.Rendered, error) {
	r := &logRenderer{}
	job, step := "\x00", "\x00"
	jobs := map[string]bool{}
	for i, l := range lines {
		if l.job != job || l.step != step {
			r.flush()
			if l.job != job {
				r.out = append(r.out, "== "+l.job)
				jobs[l.job] = true
			}
			if l.step != "UNKNOWN STEP" {
				r.out = append(r.out, "-- "+l.step)
			}
			job, step = l.job, l.step
		}
		_ = i
		r.line(l)
	}
	r.flush()
	header := fmt.Sprintf("run log: %d lines, %d jobs; timestamps omitted", len(lines), len(jobs))
	if r.setup > 0 {
		header += fmt.Sprintf("; runner setup ×%d", r.setup)
	}
	if r.blank > 0 {
		header += fmt.Sprintf("; blank lines ×%d", r.blank)
	}
	body := strings.Join(append([]string{header}, r.out...), "\n")
	if len(body) >= rawLen {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d log lines", len(lines)), Elided: true}, nil
}

func (r *logRenderer) line(l logLine) {
	t := l.text
	if strings.TrimSpace(t) == "" {
		r.blank++
		return
	}
	if title, ok := strings.CutPrefix(t, "##[group]"); ok {
		r.flushPlain()
		r.closeGroup()
		r.group = &logGroup{title: title}
		return
	}
	if t == "##[endgroup]" {
		r.closeGroup()
		return
	}
	if r.group != nil {
		if logSetupGroups[r.group.title] {
			r.setup++
			return
		}
		r.group.body = append(r.group.body, t)
		if logErrRE.MatchString(t) {
			r.group.hasEr = true
		}
		return
	}
	if (l.step == "UNKNOWN STEP" || l.step == "Set up job" || l.step == "Complete job") && logSetupRE.MatchString(t) {
		r.setup++
		return
	}
	r.plain = append(r.plain, t)
}

func (r *logRenderer) closeGroup() {
	g := r.group
	r.group = nil
	if g == nil {
		return
	}
	if logSetupGroups[g.title] {
		r.setup += len(g.body) + 1
		return
	}
	switch {
	case len(g.body) == 0:
		r.out = append(r.out, "▸ "+g.title)
	case !g.hasEr:
		r.out = append(r.out, fmt.Sprintf("▸ %s (+%d lines)", g.title, len(g.body)))
		r.collapsedGroups++
	default:
		r.out = append(r.out, "▸ "+g.title)
		r.out = append(r.out, condense(g.body, logGroupCap)...)
	}
}

func (r *logRenderer) flushPlain() {
	if len(r.plain) > 0 {
		r.out = append(r.out, condense(r.plain, logPlainCap)...)
		r.plain = nil
	}
}

// flush ends a job/step segment, closing any open group.
func (r *logRenderer) flush() {
	r.flushPlain()
	r.closeGroup()
}

// condense keeps every line of a short block. A longer one keeps its head,
// its tail and every error-looking line in between, and each omitted run is
// replaced by a counted marker.
func condense(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	head, tail := limit/6, limit/2
	var out []string
	skipped := 0
	flushSkip := func() {
		if skipped > 0 {
			out = append(out, fmt.Sprintf("…+%d lines", skipped))
			skipped = 0
		}
	}
	for i, l := range lines {
		if i < head || i >= len(lines)-tail || logErrRE.MatchString(l) {
			flushSkip()
			out = append(out, l)
			continue
		}
		skipped++
	}
	flushSkip()
	return out
}
