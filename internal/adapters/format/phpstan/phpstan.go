// Package phpstan formats PHPStan's default table output: errors grouped per
// file, one line per error carrying its line number, message and identifier,
// plus the final "Found N errors" line. Every error is kept; the agent
// boilerplate, progress bar and table borders are dropped.
package phpstan

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `phpstan` output. It holds no state.
type Formatter struct{}

// New constructs a Formatter for `phpstan`.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `phpstan` invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "phpstan"} }

var (
	borderRE   = regexp.MustCompile(`^ -{2,}(?: -{2,})* ?$`)
	progressRE = regexp.MustCompile(`^\s*\d+/\d+ \[.*\]\s+\d+%\s*$`)
	noteRE     = regexp.MustCompile(`^Note: Using configuration file `)
	statusRE   = regexp.MustCompile(`^ \[(OK|ERROR|WARNING|NOTE|CAUTION)\] (.*?)\s*$`)
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	foundRE    = regexp.MustCompile(`^Found (\d+) errors?`)
)

const instructionsHeader = "Instructions for interpreting errors"

// onlyTableFormat reports whether argv leaves PHPStan on its default table
// output: any other --error-format is a contract another tool parses.
func onlyTableFormat(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--error-format" && i+1 < len(argv):
			return argv[i+1] == "table"
		case strings.HasPrefix(a, "--error-format="):
			return strings.TrimPrefix(a, "--error-format=") == "table"
		case a == "--generate-baseline" || strings.HasPrefix(a, "--generate-baseline="):
			return false
		}
	}
	return true
}

type problem struct {
	line, msg, id string
	extra         []string
}

type fileBlock struct {
	name string
	errs []problem
}

// Aggressive groups the error tables per file.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if !onlyTableFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(ansiRE.ReplaceAllString(stdout, ""))

	var blocks []*fileBlock
	var tail []string
	dropped := 0
	sawStatus := false
	total := -1
	inInstructions := false

	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t")
		switch {
		case l == "":
			continue
		case inInstructions:
			if borderRE.MatchString(lines[i]) || statusRE.MatchString(l) {
				inInstructions = false
			} else {
				dropped++
				continue
			}
		case l == instructionsHeader:
			inInstructions = true
			dropped++
			continue
		case noteRE.MatchString(l), progressRE.MatchString(l):
			dropped++
			continue
		}
		if borderRE.MatchString(lines[i]) {
			// A table: border, header, border, rows..., border.
			blk, next, ok := parseTable(lines, i)
			if !ok {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			if blk != nil {
				blocks = append(blocks, blk)
			} else {
				// A table this package does not group (e.g. project-level
				// "Error" column): keep its rows trimmed.
				tail = append(tail, tableRows(lines[i:next])...)
			}
			i = next - 1
			continue
		}
		if m := statusRE.FindStringSubmatch(l); m != nil {
			sawStatus = true
			if fm := foundRE.FindStringSubmatch(m[2]); fm != nil {
				fmt.Sscan(fm[1], &total)
			}
			tail = append(tail, m[2])
			continue
		}
		tail = append(tail, l)
	}
	if !sawStatus {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	n := 0
	for _, b := range blocks {
		n += len(b.errs)
	}
	if total >= 0 && total != n+projectErrors(tail) {
		// The summary counts errors this parser did not account for.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for _, blk := range blocks {
		fmt.Fprintf(&b, "%s (%d)\n", blk.name, len(blk.errs))
		for _, e := range blk.errs {
			b.WriteString("  " + e.line + " " + e.msg)
			if e.id != "" {
				b.WriteString(" [" + e.id + "]")
			}
			b.WriteByte('\n')
			for _, x := range e.extra {
				b.WriteString("    " + x + "\n")
			}
		}
	}
	for _, l := range tail {
		b.WriteString(l + "\n")
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "…+%d lines (config note, progress, agent boilerplate)\n", dropped)
	}
	if stderr != "" {
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != "", Elided: true}, nil
}

// projectErrors counts rows kept from non-file tables, which the "Found N
// errors" total also includes.
func projectErrors(tail []string) int {
	n := 0
	for _, l := range tail {
		if strings.HasPrefix(l, "project: ") {
			n++
		}
	}
	return n
}

// parseTable reads the table starting at the border on lines[start]. It
// returns the grouped block (nil for a table of another shape) and the index
// just past the table.
func parseTable(lines []string, start int) (*fileBlock, int, bool) {
	// Find the end: third border (file tables) or second border (single column).
	borders := 1
	i := start + 1
	for ; i < len(lines); i++ {
		if borderRE.MatchString(lines[i]) {
			borders++
			// A table has header, rows, footer: borders at start, after the
			// header, and at the end.
			if borders == 3 {
				i++
				break
			}
		}
	}
	if borders < 3 {
		return nil, 0, false
	}
	end := i
	sep := lines[start]
	// Width of the first column from the first dash run.
	w1 := len(strings.TrimLeft(sep, " ")) - len(strings.TrimLeft(strings.TrimLeft(sep, " "), "-"))
	if w1 <= 0 || !strings.Contains(strings.TrimSpace(sep), " ") {
		return nil, end, true
	}
	hdr := lines[start+1]
	c2 := 1 + w1 + 1 // index where the second column's dashes begin
	if len(hdr) <= c2+1 || strings.TrimSpace(hdr[:c2]) != "Line" {
		return nil, end, true
	}
	blk := &fileBlock{name: strings.TrimSpace(hdr[c2+1:])}
	var cur *problem
	for _, row := range lines[start+3 : end-1] {
		if len(row) < 3 {
			continue
		}
		var cell1, cell2 string
		if len(row) > c2 {
			cell1 = strings.TrimSpace(row[:c2])
			cell2 = strings.TrimSpace(row[c2:])
		} else {
			cell1 = strings.TrimSpace(row)
		}
		switch {
		case cell1 != "":
			blk.errs = append(blk.errs, problem{line: cell1, msg: cell2})
			cur = &blk.errs[len(blk.errs)-1]
		case cur == nil:
		case strings.HasPrefix(cell2, "🪪"):
			cur.id = strings.TrimSpace(strings.TrimPrefix(cell2, "🪪"))
		case strings.HasPrefix(cell2, "💡"):
			cur.extra = append(cur.extra, cell2)
		case cell2 != "":
			if len(cur.extra) > 0 {
				cur.extra[len(cur.extra)-1] += " " + cell2
			} else {
				cur.msg += " " + cell2
			}
		}
	}
	return blk, end, true
}

// tableRows returns the content rows of a table this package does not group,
// prefixed so they are distinguishable from the status lines.
func tableRows(tbl []string) []string {
	var out []string
	for i, row := range tbl {
		if borderRE.MatchString(row) || i == 1 { // borders and the header
			continue
		}
		if t := strings.TrimSpace(row); t != "" {
			out = append(out, "project: "+t)
		}
	}
	return out
}

// Relaxed drops blank lines and progress rows, collapsing repeats.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	if !onlyTableFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if stdout == "" && stderr == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	for _, text := range []string{ansiRE.ReplaceAllString(stdout, ""), ansiRE.ReplaceAllString(stderr, "")} {
		lines := splitLines(text)
		for i := 0; i < len(lines); {
			l := strings.TrimRight(lines[i], " \t")
			if l == "" || progressRE.MatchString(l) {
				i++
				continue
			}
			j := i + 1
			for j < len(lines) && strings.TrimRight(lines[j], " \t") == l {
				j++
			}
			b.WriteString(l)
			if n := j - i; n > 1 {
				fmt.Fprintf(&b, " ×%d", n)
			}
			b.WriteByte('\n')
			i = j
		}
	}
	if b.Len() == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != ""}, nil
}

func readStreams(in format.Input) (stdout, stderr string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return "", "", fmt.Errorf("phpstan: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("phpstan: reading stderr: %w", err)
	}
	return string(o), string(e), nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
