// Package sqlite3 compacts the output of the sqlite3 command-line shell for
// validated result-set shapes: -box, -table, -markdown and -column tables lose
// only their borders and padding, -line records lose only name padding, and
// consistent `|`-list or CSV rows are passed through. A query result is the
// answer the agent asked for, so rows are only dropped above rowCap (recordCap
// -line records), always with an explicit "…+N sqlite3 rows" marker. Dot
// commands (.tables, .schema), JSON modes, errors and anything it cannot prove
// is a complete result set are declined.
package sqlite3

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Nothing is omitted at or below the cap; above it the first *Head and last
// *Tail entries are kept and the remainder is counted.
const (
	rowCap     = 200
	rowHead    = 150
	rowTail    = 50
	recordCap  = 100
	recordHead = 70
	recordTail = 30
)

// Formatter renders `sqlite3` output.
type Formatter struct{}

// New constructs a sqlite3 Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims the `sqlite3` program; there is no subcommand.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "sqlite3"} }

// Aggressive compacts a successful run's stdout.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("sqlite3: reading stdout: %w", err)
	}
	lines := splitLines(raw)
	if len(lines) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var (
		out     []string
		omitted int
		unit    = "rows"
		ok      bool
	)
	switch {
	case strings.HasPrefix(lines[0], "╭"):
		out, omitted, ok = box(lines)
	case strings.HasPrefix(lines[0], "+") && isBorder(lines[0], '+', '-'):
		out, omitted, ok = table(lines)
	case strings.HasPrefix(lines[0], "|") && len(lines) > 1 && isMarkdownRule(lines[1]):
		out, omitted, ok = markdown(lines)
	case columnRule(lines) >= 0:
		out, omitted, ok = column(lines, columnRule(lines))
	case isLineRecord(lines):
		out, omitted, ok = lineRecords(lines)
		unit = "records"
	default:
		out, omitted, ok = listRows(lines, in.Argv)
	}
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := strings.Join(out, "\n") + "\n"
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:   []byte(body),
		Note:   fmt.Sprintf("sqlite3: omitted %d %s", omitted, unit),
		Elided: omitted > 0,
	}, nil
}

// Relaxed never applies: sqlite3 prints data, and the only safe reductions are
// the validated shapes above.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// bound applies the row cap to already-compact rows.
func bound(head []string, rows []string) ([]string, int) {
	out := append([]string(nil), head...)
	if len(rows) <= rowCap {
		return append(out, rows...), 0
	}
	omitted := len(rows) - rowHead - rowTail
	out = append(out, rows[:rowHead]...)
	out = append(out, fmt.Sprintf("…+%d sqlite3 rows", omitted))
	return append(out, rows[len(rows)-rowTail:]...), omitted
}

// box handles -box: ╭─┬─╮ / │ header │ / ╞═╪═╡ / │ rows │ / ╰─┴─╯.
func box(lines []string) ([]string, int, bool) {
	n := len(lines)
	if n < 4 || !strings.HasPrefix(lines[2], "╞") || !strings.HasPrefix(lines[n-1], "╰") {
		return nil, 0, false
	}
	cols := strings.Count(lines[1], "│") - 1
	if cols < 1 {
		return nil, 0, false
	}
	var rows []string
	for _, l := range lines[3 : n-1] {
		if !strings.HasPrefix(l, "│") || strings.Count(l, "│")-1 != cols {
			return nil, 0, false
		}
		rows = append(rows, cells(l, "│"))
	}
	out, omitted := bound([]string{cells(lines[1], "│")}, rows)
	return out, omitted, true
}

// table handles -table: +---+ / | header | / +---+ / | rows | / +---+.
func table(lines []string) ([]string, int, bool) {
	n := len(lines)
	if n < 4 || !isBorder(lines[2], '+', '-') || !isBorder(lines[n-1], '+', '-') {
		return nil, 0, false
	}
	pipes := strings.Count(lines[1], "|")
	var rows []string
	for _, l := range lines[3 : n-1] {
		if !strings.HasPrefix(l, "|") || strings.Count(l, "|") != pipes {
			return nil, 0, false
		}
		rows = append(rows, cells(l, "|"))
	}
	out, omitted := bound([]string{cells(lines[1], "|")}, rows)
	return out, omitted, true
}

// markdown handles -markdown: | header | / |---|---| / | rows |.
func markdown(lines []string) ([]string, int, bool) {
	pipes := strings.Count(lines[0], "|")
	var rows []string
	for _, l := range lines[2:] {
		if !strings.HasPrefix(l, "|") || strings.Count(l, "|") != pipes {
			return nil, 0, false
		}
		rows = append(rows, cells(l, "|"))
	}
	out, omitted := bound([]string{cells(lines[0], "|")}, rows)
	return out, omitted, true
}

func isMarkdownRule(l string) bool {
	return strings.HasPrefix(l, "|-") && isBorder(l, '|', '-')
}

// columnRule returns the index of the `---  ---` rule line of -column output
// (always line 1, after the header) or -1.
func columnRule(lines []string) int {
	if len(lines) < 2 {
		return -1
	}
	rule := lines[1]
	if !strings.HasPrefix(rule, "-") || strings.Trim(rule, "- ") != "" || !strings.Contains(rule, "-") {
		return -1
	}
	return 1
}

// column handles -column -header: positional cells split by the rule line.
func column(lines []string, rule int) ([]string, int, bool) {
	spans := spansOf(lines[rule])
	if len(spans) == 0 {
		return nil, 0, false
	}
	var rows []string
	for i, l := range lines {
		if i == rule {
			continue
		}
		cs, ok := slice(l, spans)
		if !ok {
			return nil, 0, false
		}
		if i == 0 {
			continue
		}
		rows = append(rows, cs)
	}
	head, _ := slice(lines[0], spans)
	out, omitted := bound([]string{head}, rows)
	return out, omitted, true
}

type span struct{ from, to int }

// spansOf returns the rune ranges of the dash runs in a rule line.
func spansOf(rule string) []span {
	var spans []span
	start := -1
	i := 0
	for _, r := range rule {
		if r == '-' {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			spans = append(spans, span{start, i})
			start = -1
		}
		i++
	}
	if start >= 0 {
		spans = append(spans, span{start, i})
	}
	return spans
}

// slice cuts a line at the spans and rejects it unless every gap between
// spans is blank, which proves the line is one aligned row.
func slice(line string, spans []span) (string, bool) {
	runes := []rune(line)
	parts := make([]string, len(spans))
	prev := 0
	for i, s := range spans {
		if s.from > len(runes) {
			parts[i] = ""
			continue
		}
		if strings.TrimSpace(string(runes[prev:min(s.from, len(runes))])) != "" {
			return "", false
		}
		to := min(s.to, len(runes))
		parts[i] = strings.TrimSpace(string(runes[s.from:to]))
		prev = to
	}
	if prev < len(runes) && strings.TrimSpace(string(runes[prev:])) != "" {
		return "", false
	}
	return strings.Join(parts, "|"), true
}

// listRows bounds default list (`a|b|c`) and CSV output by lines when every
// line carries the same separator count, which is how consistent rows look.
// Dot-command text (.schema, .tables) never has that shape and is declined.
func listRows(lines []string, argv []string) ([]string, int, bool) {
	if len(lines) <= rowCap {
		return nil, 0, false
	}
	sep := "|"
	for _, a := range argv {
		if a == "-csv" || a == "--csv" {
			sep = ","
		}
		if a == "-json" || a == "-jsonl" || a == "-html" || a == "-xml" || a == "-tcl" {
			return nil, 0, false
		}
	}
	want := separators(lines[0], sep)
	if want < 0 || want == 0 && sep == "|" {
		return nil, 0, false
	}
	for _, l := range lines {
		if separators(l, sep) != want {
			return nil, 0, false
		}
	}
	omitted := len(lines) - rowHead - rowTail
	out := append([]string(nil), lines[:rowHead]...)
	out = append(out, fmt.Sprintf("…+%d sqlite3 rows", omitted))
	return append(out, lines[len(lines)-rowTail:]...), omitted, true
}

// fieldSep splits one -line row at its name/value separator (": " in current
// sqlite3, " = " in older releases). The name must be a single token.
func fieldSep(l string) (idx, sepLen int, ok bool) {
	for _, sep := range []string{": ", " = "} {
		i := strings.Index(l, sep)
		if i <= 0 {
			continue
		}
		name := strings.TrimLeft(l[:i], " ")
		if name != "" && !strings.ContainsAny(name, " \t:=") {
			return i, len(sep), true
		}
	}
	return 0, 0, false
}

// separators counts sep outside double quotes. An odd number of quotes means a
// quoted field continues onto the next line, so the line is not a whole row: -1.
func separators(l, sep string) int {
	if sep != "," {
		return strings.Count(l, sep)
	}
	n, inQuote := 0, false
	for _, r := range l {
		switch r {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				n++
			}
		}
	}
	if inQuote {
		return -1
	}
	return n
}

// isLineRecord reports whether the output opens like -line output.
func isLineRecord(lines []string) bool {
	_, _, ok := fieldSep(lines[0])
	return ok && !strings.Contains(lines[0], "|")
}

// lineRecords compacts -line output: records are blank-line separated and every
// line of a record has its separator at one column. Any other shape (for
// instance a value spanning lines) is declined.
func lineRecords(lines []string) ([]string, int, bool) {
	var recs [][]string
	var cur []string
	for _, l := range lines {
		if l == "" {
			if len(cur) == 0 {
				return nil, 0, false
			}
			recs = append(recs, cur)
			cur = nil
			continue
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		recs = append(recs, cur)
	}
	texts := make([]string, 0, len(recs))
	for _, rec := range recs {
		width := -1
		out := make([]string, 0, len(rec))
		for _, l := range rec {
			idx, _, ok := fieldSep(l)
			if !ok || (width >= 0 && idx != width) {
				return nil, 0, false
			}
			width = idx
			out = append(out, strings.TrimLeft(l[:idx], " ")+l[idx:])
		}
		texts = append(texts, strings.Join(out, "\n"))
	}
	omitted := 0
	if len(texts) > recordCap {
		omitted = len(texts) - recordHead - recordTail
		kept := append([]string(nil), texts[:recordHead]...)
		kept = append(kept, fmt.Sprintf("…+%d sqlite3 records", omitted))
		texts = append(kept, texts[len(texts)-recordTail:]...)
	}
	for i := range texts[:len(texts)-1] {
		texts[i] += "\n" // keep the blank line between records
	}
	return texts, omitted, true
}

// cells strips the outer separators and the padding around every cell.
func cells(line, sep string) string {
	line = strings.TrimSuffix(strings.TrimPrefix(line, sep), sep)
	parts := strings.Split(line, sep)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "|")
}

func isBorder(l string, corner, dash rune) bool {
	if utf8.RuneCountInString(l) < 3 {
		return false
	}
	for _, r := range l {
		if r != corner && r != dash {
			return false
		}
	}
	return true
}

func splitLines(raw []byte) []string {
	text := strings.TrimRight(string(raw), "\r\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
