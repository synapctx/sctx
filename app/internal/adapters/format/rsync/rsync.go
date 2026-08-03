// Package rsync implements a format.Formatter for `rsync`.
//
// rsync was the top real entry on sctx's coverage-gap meter (32 delegations in 7 days),
// and its output is unusually wasteful for an agent: one line per transferred path, and
// with --progress a SECOND line per path carrying a byte count, a percentage, a rate and
// an ETA for a transfer that has already finished. None of that survives usefully into a
// context window — what an agent needs is what changed, what was deleted, whether anything
// failed, and roughly how much moved.
//
// Written against real GNU rsync 3.4.1 output rather than from memory, and the difference
// mattered: macOS ships openrsync, whose header is `Transfer starting: N files` where GNU
// emits `sending incremental file list`. A formatter built on the macOS dialect would have
// silently declined on every Linux host — which is where the actual workload runs.
package rsync

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `rsync` output.
type Formatter struct{}

// New constructs an rsync Formatter.
func New() format.Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "rsync"}
}

const (
	// maxListed caps how many paths are named per category before eliding. An agent
	// deciding what to do next needs a representative handful and an honest count, not
	// nine hundred paths. Measured at 12 this saved only 20% on a routine sync, because
	// naming twelve siblings costs about what the original list did; six keeps the sample
	// useful and the elision counter carries the rest.
	maxListed = 6
	// minLines below which the raw output is already compact enough that reformatting
	// cannot pay for itself; the tier declines so the chain leaves it alone.
	minLines = 4
)

var (
	// headerRe matches the file-list banner in both dialects. openrsync's wording differs
	// from GNU's, and both appear in practice (developer laptops vs Linux CI/servers).
	headerRe = regexp.MustCompile(`^(sending|receiving) incremental file list$|^Transfer starting: \d+ files?$`)

	// progressRe matches a --progress line: leading whitespace, a byte count with optional
	// thousands separators, then a percentage. These describe a transfer that has already
	// completed by the time output is read, so they are dropped entirely.
	progressRe = regexp.MustCompile(`^\s+[0-9][0-9,._]*\s+\d+%`)

	// summaryRe matches `sent N bytes  received M bytes  R bytes/sec`.
	summaryRe = regexp.MustCompile(`^sent ([0-9,]+) bytes\s+received ([0-9,]+) bytes\s+(.*)$`)

	// totalRe matches `total size is N  speedup is M` plus an optional ` (DRY RUN)`.
	totalRe = regexp.MustCompile(`^total size is ([0-9,]+)\s+speedup is ([0-9.]+)(.*)$`)

	// itemizeRe matches an --itemize-changes line: an 11-character change code then a
	// path. The code's first two characters carry the useful part (`>f` a received file,
	// `cd` a created dir, `.f` attributes only), so it is reduced rather than dropped.
	itemizeRe = regexp.MustCompile(`^([<>ch.*][fdLDS][a-zA-Z.+?]{9}) (.+)$`)

	// statsLineRe matches a line from the --stats block, which is 13 lines of which an
	// agent needs three.
	statsLineRe = regexp.MustCompile(`^(Number of|Total|Literal data|Matched data|File list)`)

	// errorRe matches rsync's own diagnostics. These are NEVER elided: reporting a
	// partial sync as a clean one is the failure that loses data.
	//
	// Two shapes, both found by running the real binaries rather than by reading docs.
	// openrsync (macOS) prefixes its PID: `rsync(18346): error: /path: (l)stat: No such
	// file or directory`, so the optional `(\d+)` group is required or that line is
	// classified as a transferred PATH.
	//
	// The alternation must also allow a SPACE after the program name. `rsync error: some
	// files/attrs were not transferred ... (code 23)` — the line that carries the exit
	// code — begins `rsync ` and an earlier pattern requiring `rsync:` missed it, so it
	// fell through and was counted as a TRANSFERRED FILE. The render then read
	// "1 transferred" for a run that transferred nothing and failed. The test did not
	// catch it because asserting Contains("code 23") was satisfied by the misclassified
	// line itself.
	errorRe = regexp.MustCompile(`^(rsync|openrsync)(\(\d+\))?(:| error| warning)|^(IO error|WARNING|ERROR)\b`)
)

// parsed is the structured view of one rsync run's output.
type parsed struct {
	transferred []string // plain paths, or itemized paths that moved data
	deleted     []string
	dirs        []string // directory entries (trailing '/'), counted not listed
	attrOnly    []string // itemized lines that changed attributes only, no data
	errors      []string // verbatim, always retained
	progress    int      // --progress lines dropped
	statsKept   []string // the few --stats lines worth keeping
	statsDrop   int      // --stats lines dropped
	sentBytes   string
	recvBytes   string
	rate        string
	totalSize   string
	speedup     string
	dryRun      bool
	sawHeader   bool
	sawSummary  bool
	rawLines    int
	other       []string // unrecognised lines, kept so nothing vanishes silently
}

// parse reads rsync output into a parsed. It classifies rather than interprets: any line it
// does not recognise goes to `other` and is emitted, because a formatter that silently
// discards what it failed to understand is indistinguishable from one that works.
func parse(r io.Reader) (*parsed, error) {
	p := &parsed{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		p.rawLines++
		switch {
		case strings.TrimSpace(line) == "":
			// blank separator: carries no information
		case headerRe.MatchString(line):
			p.sawHeader = true
		case errorRe.MatchString(line):
			p.errors = append(p.errors, line)
		case progressRe.MatchString(line):
			p.progress++
		case summaryRe.MatchString(line):
			m := summaryRe.FindStringSubmatch(line)
			p.sentBytes, p.recvBytes, p.rate = m[1], m[2], strings.TrimSpace(m[3])
			p.sawSummary = true
		case totalRe.MatchString(line):
			m := totalRe.FindStringSubmatch(line)
			p.totalSize, p.speedup = m[1], m[2]
			if strings.Contains(m[3], "DRY RUN") {
				p.dryRun = true
			}
		case strings.HasPrefix(line, "deleting "):
			p.deleted = append(p.deleted, strings.TrimPrefix(line, "deleting "))
		case itemizeRe.MatchString(line):
			m := itemizeRe.FindStringSubmatch(line)
			code, path := m[1], m[2]
			switch {
			case strings.HasSuffix(path, "/"):
				p.dirs = append(p.dirs, path)
			case code[0] == '.':
				// A leading '.' means no data was transferred; only attributes changed.
				// Distinguishing these keeps the transferred count honest.
				p.attrOnly = append(p.attrOnly, path)
			default:
				p.transferred = append(p.transferred, path)
			}
		case statsLineRe.MatchString(line):
			if keepStat(line) {
				p.statsKept = append(p.statsKept, line)
			} else {
				p.statsDrop++
			}
		case strings.HasSuffix(line, "/") && !strings.HasPrefix(line, " "):
			p.dirs = append(p.dirs, line)
		case !strings.HasPrefix(line, " "):
			p.transferred = append(p.transferred, line)
		default:
			p.other = append(p.other, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// keepStat selects the --stats lines that answer a question an agent would ask. The block
// also reports file-list generation and transfer times, literal versus matched data, and
// byte totals already present in the summary line.
func keepStat(line string) bool {
	for _, pre := range []string{
		"Number of files:",
		"Number of created files:",
		"Number of deleted files:",
		"Number of regular files transferred:",
		"Total file size:",
	} {
		if strings.HasPrefix(line, pre) {
			return true
		}
	}
	return false
}

// looksLikeRsync reports whether the output is recognisably rsync's. Without a header, a
// summary line, or a diagnostic, this is something else entirely (a bare `rsync --help`,
// or a wrapper's output) and the tier must decline rather than reformat unknown text.
func (p *parsed) looksLikeRsync() bool {
	return p.sawHeader || p.sawSummary || len(p.errors) > 0
}

// Aggressive renders the structured summary.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	p, err := parse(io.MultiReader(in.Stdout, in.Stderr))
	if err != nil {
		return format.Rendered{}, err
	}
	if !p.looksLikeRsync() {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Output already this small cannot be usefully compressed, and the tier chain rejects
	// a render that is not smaller as an anomaly. Declining is the honest response.
	if p.rawLines < minLines && len(p.errors) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder

	// Errors first and verbatim. On a non-zero exit this is the only part that matters,
	// and a partial sync reported as a clean one is how data gets lost.
	for _, e := range p.errors {
		b.WriteString(e)
		b.WriteByte('\n')
	}

	b.WriteString(headline(p, in.ExitCode))
	b.WriteByte('\n')

	writeGroup(&b, "+", p.transferred)
	writeGroup(&b, "-", p.deleted)
	writeGroup(&b, "~", p.attrOnly)

	for _, s := range p.statsKept {
		b.WriteString("  ")
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if p.statsDrop > 0 {
		fmt.Fprintf(&b, "  (+%d more --stats lines)\n", p.statsDrop)
	}
	for _, o := range p.other {
		b.WriteString(o)
		b.WriteByte('\n')
	}

	return format.Rendered{Body: []byte(b.String()), FoldStderr: true}, nil
}

// headline is the one line that answers "what happened".
func headline(p *parsed, exitCode int) string {
	var parts []string
	if p.dryRun {
		parts = append(parts, "DRY RUN")
	}
	parts = append(parts, fmt.Sprintf("%d transferred", len(p.transferred)))
	if len(p.deleted) > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", len(p.deleted)))
	}
	if len(p.attrOnly) > 0 {
		parts = append(parts, fmt.Sprintf("%d attr-only", len(p.attrOnly)))
	}
	if n := len(p.dirs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "dir", "dirs")))
	}
	if p.progress > 0 {
		// Named explicitly: an elision the reader cannot see is one they cannot trust.
		parts = append(parts, fmt.Sprintf("%d progress lines elided", p.progress))
	}
	if p.sentBytes != "" || p.recvBytes != "" {
		parts = append(parts, fmt.Sprintf("sent %s recv %s", humanBytes(p.sentBytes), humanBytes(p.recvBytes)))
	}
	if p.totalSize != "" {
		parts = append(parts, fmt.Sprintf("total %s", humanBytes(p.totalSize)))
	}
	if p.speedup != "" {
		parts = append(parts, "speedup "+p.speedup)
	}
	if exitCode != 0 {
		parts = append(parts, fmt.Sprintf("EXIT %d", exitCode))
	}
	return "rsync: " + strings.Join(parts, " · ")
}

// plural picks the right noun for a count, because "1 dirs" reads as a bug in the tool.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// writeGroup emits up to maxListed paths under a marker, with an explicit count for the
// remainder. Paths are sorted so two runs of the same sync produce the same rendering.
func writeGroup(b *strings.Builder, marker string, paths []string) {
	if len(paths) == 0 {
		return
	}
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)
	shown := sorted
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	fmt.Fprintf(b, "  %s %s", marker, strings.Join(shown, ", "))
	if rest := len(sorted) - len(shown); rest > 0 {
		fmt.Fprintf(b, " …+%d", rest)
	}
	b.WriteByte('\n')
}

// humanBytes shortens rsync's comma-grouped byte counts. rsync already prints thousands
// separators, which cost tokens without adding meaning at this scale.
func humanBytes(s string) string {
	if s == "" {
		return "0B"
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	if err != nil {
		return s
	}
	switch {
	case n < 1024:
		return fmt.Sprintf("%.0fB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", n/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", n/(1024*1024))
	default:
		return fmt.Sprintf("%.2fGB", n/(1024*1024*1024))
	}
}

// Relaxed drops the pure noise — progress lines and the redundant part of the --stats
// block — while leaving every path and diagnostic exactly as rsync wrote it. It is the
// fallback for output the aggressive tier could not fully classify.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	var b strings.Builder
	var dropped int
	sc := bufio.NewScanner(io.MultiReader(in.Stdout, in.Stderr))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if progressRe.MatchString(line) {
			dropped++
			continue
		}
		if statsLineRe.MatchString(line) && !keepStat(line) {
			dropped++
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return format.Rendered{}, err
	}
	if dropped == 0 {
		// Nothing was removed, so this render is not smaller and the chain would reject
		// it as an anomaly. Say so plainly instead.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	fmt.Fprintf(&b, "(+%d noise lines elided)\n", dropped)
	return format.Rendered{Body: []byte(b.String()), FoldStderr: true}, nil
}
