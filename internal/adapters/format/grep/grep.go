// Package grep formats grep and ripgrep match output. Aggressive parses the
// classic "path:line:content" and ripgrep's grouped heading formats and
// re-renders them grouped by file with duplicate collapsing and hard caps.
// Relaxed falls back to consecutive-line dedupe and length/line caps when the
// output isn't recognizable match-line output.
package grep

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

const (
	maxMatchesPerFile = 8
	maxFiles          = 40
	maxContentLen     = 160
	maxRelaxedLineLen = 200
	maxRelaxedLines   = 300
)

var (
	// rePathLineContent matches "path:line:content" (grep -rn, rg -n/--no-heading).
	rePathLineContent = regexp.MustCompile(`^(.+?):(\d+):(.*)$`)
	// reLineContent matches "line:content" (rg grouped body lines, or grep -n on a single file).
	reLineContent = regexp.MustCompile(`^(\d+):(.*)$`)
)

// formatter implements format.Formatter for both grep and rg; the two names
// are registered as separate descriptors sharing this implementation.
type formatter struct {
	command string
}

// New returns the grep formatter.
func New() format.Formatter {
	return &formatter{command: "grep"}
}

// All returns both the grep and rg formatters.
func All() []format.Formatter {
	return []format.Formatter{
		&formatter{command: "grep"},
		&formatter{command: "rg"},
	}
}

func (f *formatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

// fileGroup holds every match line found for one file, in encounter order.
type fileGroup struct {
	path    string
	matches []matchLine
}

type matchLine struct {
	lineNo  string
	content string
}

// Aggressive re-renders recognizable match-line output grouped by file. It
// returns format.ErrTierInapplicable for empty output or output that isn't
// match-line shaped (e.g. grep -l file lists, grep -c counts), which are
// already compact.
func (f *formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, err
	}
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	groups, total, ok := parseMatches(raw, in.Argv)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	body := renderGroups(groups, total)
	if len(body) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var note string
	if len(groups) == 1 && groups[0].path == "" {
		note = fmt.Sprintf("%d %s", total, pluralize(total, "match", "matches"))
	} else {
		note = fmt.Sprintf("%d %s in %d %s", total, pluralize(total, "match", "matches"), len(groups), pluralize(len(groups), "file", "files"))
	}

	return format.Rendered{Body: body, Note: note}, nil
}

// Relaxed collapses consecutive duplicate lines, truncates long lines, and
// caps the total number of lines emitted. Stderr is left untouched by this
// tier (FoldStderr is false) since permission-denied warnings matter.
func (f *formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, err
	}
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := splitLines(raw)
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		n := j - i
		line := truncate(lines[i], maxRelaxedLineLen)
		if n > 1 {
			line = fmt.Sprintf("%s ×%d", line, n)
		}
		out = append(out, line)
		i = j
	}

	var extra int
	if len(out) > maxRelaxedLines {
		extra = len(out) - maxRelaxedLines
		out = out[:maxRelaxedLines]
	}

	var b strings.Builder
	for _, l := range out {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if extra > 0 {
		fmt.Fprintf(&b, "…+%d more lines\n", extra)
	}

	body := []byte(b.String())
	if len(body) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: body}, nil
}

// parseMatches classifies raw's lines and groups match lines by file. It
// returns ok=false when no line looks like match output (e.g. -l/-c output).
func parseMatches(raw []byte, argv []string) ([]*fileGroup, int, bool) {
	lines := splitLines(raw)

	const (
		kindBlank byte = iota
		kindPathLine
		kindLine
		kindHead
	)
	kinds := make([]byte, len(lines))
	var nPathLine, nLine, nHead int
	for i, ln := range lines {
		switch {
		case ln == "" || ln == "--":
			kinds[i] = kindBlank
		case rePathLineContent.MatchString(ln):
			kinds[i] = kindPathLine
			nPathLine++
		case reLineContent.MatchString(ln):
			kinds[i] = kindLine
			nLine++
		default:
			kinds[i] = kindHead
			nHead++
		}
	}

	if nPathLine == 0 && nLine == 0 {
		return nil, 0, false
	}

	var mode string
	switch {
	case nHead > 0 && nLine > 0:
		mode = "grouped"
	case nPathLine > 0:
		mode = "flatPathLine"
	default:
		mode = "flatLine"
	}

	var order []string
	byPath := map[string]*fileGroup{}
	total := 0
	addMatch := func(path, lineNo, content string) {
		g, ok := byPath[path]
		if !ok {
			g = &fileGroup{path: path}
			byPath[path] = g
			order = append(order, path)
		}
		g.matches = append(g.matches, matchLine{lineNo: lineNo, content: content})
		total++
	}

	switch mode {
	case "flatPathLine":
		for i, ln := range lines {
			if kinds[i] != kindPathLine {
				continue
			}
			m := rePathLineContent.FindStringSubmatch(ln)
			addMatch(m[1], m[2], m[3])
		}
	case "flatLine":
		path := guessPath(argv)
		for i, ln := range lines {
			if kinds[i] != kindLine {
				continue
			}
			m := reLineContent.FindStringSubmatch(ln)
			addMatch(path, m[1], m[2])
		}
	case "grouped":
		current := ""
		haveCurrent := false
		for i, ln := range lines {
			switch kinds[i] {
			case kindBlank:
				current, haveCurrent = "", false
			case kindPathLine:
				m := rePathLineContent.FindStringSubmatch(ln)
				addMatch(m[1], m[2], m[3])
				current, haveCurrent = m[1], true
			case kindHead:
				current, haveCurrent = ln, true
			case kindLine:
				if haveCurrent {
					m := reLineContent.FindStringSubmatch(ln)
					addMatch(current, m[1], m[2])
				}
			}
		}
	}

	if len(order) == 0 {
		return nil, 0, false
	}
	groups := make([]*fileGroup, 0, len(order))
	for _, p := range order {
		groups = append(groups, byPath[p])
	}
	return groups, total, true
}

// renderGroups renders groups grouped-by-file, capped at maxFiles files and
// maxMatchesPerFile matches per file, with explicit "+N more" markers
// whenever anything is elided.
func renderGroups(groups []*fileGroup, total int) []byte {
	var b strings.Builder
	shown := 0
	for _, g := range groups {
		if shown >= maxFiles {
			break
		}
		if shown > 0 {
			b.WriteByte('\n')
		}
		writeFileGroup(&b, g)
		shown++
	}
	if len(groups) > maxFiles {
		remainingFiles := groups[maxFiles:]
		remainingMatches := 0
		for _, g := range remainingFiles {
			remainingMatches += len(g.matches)
		}
		fmt.Fprintf(&b, "\n…+%d more files (%d matches)\n", len(groups)-maxFiles, remainingMatches)
	}
	return []byte(b.String())
}

// collapsedRow is one rendered line for a file: one or more identical
// content lines collapsed into a single row with their line numbers listed.
type collapsedRow struct {
	lineNos []string
	content string
	count   int
}

func writeFileGroup(b *strings.Builder, g *fileGroup) {
	count := len(g.matches)
	if g.path != "" {
		fmt.Fprintf(b, "%s (%d)\n", g.path, count)
	}

	var rows []*collapsedRow
	byContent := map[string]*collapsedRow{}
	for _, m := range g.matches {
		content := truncate(strings.TrimLeft(m.content, " \t"), maxContentLen)
		if r, ok := byContent[content]; ok {
			r.lineNos = append(r.lineNos, m.lineNo)
			r.count++
			continue
		}
		r := &collapsedRow{lineNos: []string{m.lineNo}, content: content, count: 1}
		byContent[content] = r
		rows = append(rows, r)
	}

	shown := 0
	matchesShown := 0
	for _, r := range rows {
		if shown >= maxMatchesPerFile {
			break
		}
		if r.count > 1 {
			fmt.Fprintf(b, "  %s: %s ×%d\n", strings.Join(r.lineNos, ","), r.content, r.count)
		} else {
			fmt.Fprintf(b, "  %s: %s\n", r.lineNos[0], r.content)
		}
		matchesShown += r.count
		shown++
	}
	if shown < len(rows) {
		fmt.Fprintf(b, "  …+%d more\n", count-matchesShown)
	}
}

// guessPath makes a best-effort attempt to name the file being searched when
// output has no path prefix (grep -n on a single file): the last non-flag
// argument, skipping argv[0].
func guessPath(argv []string) string {
	var last string
	for i, a := range argv {
		if i == 0 {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		last = a
	}
	return last
}

func splitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
