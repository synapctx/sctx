// Package clickhouse compacts the output of `clickhouse-client --query`. A
// result set is the answer the agent asked for, so rows are only dropped above
// rowCap, with an explicit "…+N clickhouse rows" marker. Pretty/PrettyCompact
// tables lose their box drawing, row numbers and padding; TSV/CSV output (the
// default when stdout is not a terminal) is already lossless and is only
// bounded. On a failed query the one thing removed is the parser's "Expected
// one of: ..." token list, cut to its first expectedKeep entries with a count
// of the rest; the error code, message, position and error name are kept.
package clickhouse

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Nothing is omitted at or below the cap; above it the first rowHead and last
// rowTail rows are kept and the remainder is counted.
const (
	rowCap       = 200
	rowHead      = 150
	rowTail      = 50
	expectedKeep = 8
)

var rowRE = regexp.MustCompile(`^ *[0-9]+\. │`)

// Formatter renders `clickhouse-client` output.
type Formatter struct{}

// New constructs a clickhouse-client Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims the `clickhouse-client` program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "clickhouse-client"} }

// Interactive reports whether argv opens the REPL: no --query/-q/--queries-file
// and no `-n`-style multiquery script on stdin cannot be told apart, so only an
// invocation without any query option is treated as interactive.
func Interactive(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		switch {
		case a == "-q", a == "--query", strings.HasPrefix(a, "--query="), a == "--queries-file", strings.HasPrefix(a, "--queries-file="),
			a == "--version", a == "-V", a == "--help", a == "--help-all":
			return false
		}
	}
	return true
}

// Aggressive compacts a successful run's stdout.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("clickhouse: reading stdout: %w", err)
	}
	lines := splitLines(raw)
	if len(lines) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var (
		out     []string
		omitted int
		ok      bool
	)
	switch {
	case isPrettyCompact(lines):
		out, omitted, ok = prettyCompact(lines)
	case isPretty(lines):
		out, omitted, ok = pretty(lines)
	case strings.Contains(lines[0], "\t"):
		out, omitted, ok = tsv(lines)
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
		Note:   fmt.Sprintf("clickhouse: omitted %d rows", omitted),
		Elided: omitted > 0,
	}, nil
}

// Relaxed shortens the parser's "Expected one of" list on a failed query.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("clickhouse: reading stderr: %w", err)
	}
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("clickhouse: reading stdout: %w", err)
	}
	if len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	short, dropped := shortenExpected(string(stderr))
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := string(stdout)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return format.Rendered{Body: []byte(body + short), FoldStderr: true, Elided: true,
		Note: fmt.Sprintf("clickhouse: %d expected tokens omitted", dropped)}, nil
}

// shortenExpected keeps the first expectedKeep entries of "Expected one of: a,
// b, c, ..." and counts the rest. Entries are split on ", ", which is also how
// ClickHouse joins them; an entry that itself contains ", " only makes the
// count approximate, never the kept text wrong.
func shortenExpected(s string) (string, int) {
	const marker = "Expected one of: "
	start := strings.Index(s, marker)
	if start < 0 {
		return s, 0
	}
	listStart := start + len(marker)
	end := strings.LastIndex(s, ". (")
	if end < listStart {
		return s, 0
	}
	items := strings.Split(s[listStart:end], ", ")
	if len(items) <= expectedKeep {
		return s, 0
	}
	dropped := len(items) - expectedKeep
	return s[:listStart] + strings.Join(items[:expectedKeep], ", ") + fmt.Sprintf(", …+%d expected tokens", dropped) + s[end:], dropped
}

// tsv bounds tab-separated output; at or below the cap it is already lossless.
func tsv(lines []string) ([]string, int, bool) {
	if len(lines) <= rowCap {
		return nil, 0, false
	}
	omitted := len(lines) - rowHead - rowTail
	out := append([]string(nil), lines[:rowHead]...)
	out = append(out, fmt.Sprintf("…+%d clickhouse rows", omitted))
	return append(out, lines[len(lines)-rowTail:]...), omitted, true
}

func isPrettyCompact(lines []string) bool {
	return strings.Contains(lines[0], "┌─") && strings.HasSuffix(lines[0], "┐")
}

func isPretty(lines []string) bool {
	return strings.Contains(lines[0], "┏━") && strings.HasSuffix(lines[0], "┓")
}

// prettyCompact handles `┌─id─┬─name─┐ / N. │ a │ b │ / └──┴──┘`.
func prettyCompact(lines []string) ([]string, int, bool) {
	header := names(lines[0])
	bottom := -1
	for i, l := range lines {
		if strings.Contains(l, "└─") && strings.HasSuffix(l, "┘") {
			bottom = i
		}
	}
	if header == "" || bottom < 2 {
		return nil, 0, false
	}
	var rows []string
	for _, l := range lines[1:bottom] {
		if !rowRE.MatchString(l) || strings.Count(l, "│") != strings.Count(header, "|")+2 {
			return nil, 0, false
		}
		rows = append(rows, rowCells(l))
	}
	return finish(header, rows, lines[bottom+1:])
}

// pretty handles `┏━┳━┓ ┃ id ┃ / ┡━╇━┩ / N. │ a │ / ├─┼─┤ / └─┴─┘`. For long
// results ClickHouse repeats the header under the rows (`┢━╈━┪ ┃ id ┃ ┗━┻━┛`);
// that repeat is dropped only when it equals the real header.
func pretty(lines []string) ([]string, int, bool) {
	if len(lines) < 4 || !strings.HasPrefix(strings.TrimLeft(lines[1], " "), "┃") || !strings.Contains(lines[2], "┡") {
		return nil, 0, false
	}
	header := cellsOf(strings.TrimLeft(lines[1], " "), "┃")
	bottom := -1
	for i, l := range lines {
		if (strings.Contains(l, "└─") && strings.HasSuffix(l, "┘")) || (strings.Contains(l, "┗━") && strings.HasSuffix(l, "┛")) {
			bottom = i
		}
	}
	if bottom < 4 {
		return nil, 0, false
	}
	end := bottom
	if bottom >= 2 && strings.Contains(lines[bottom-2], "┢") && strings.HasPrefix(strings.TrimLeft(lines[bottom-1], " "), "┃") {
		if cellsOf(strings.TrimLeft(lines[bottom-1], " "), "┃") != header {
			return nil, 0, false
		}
		end = bottom - 2
	}
	var rows []string
	for _, l := range lines[3:end] {
		switch {
		case rowRE.MatchString(l):
			if strings.Count(l, "│") != strings.Count(header, "|")+2 {
				return nil, 0, false
			}
			rows = append(rows, rowCells(l))
		case strings.Contains(l, "├─") && strings.HasSuffix(l, "┤"):
		default:
			return nil, 0, false
		}
	}
	return finish(header, rows, lines[bottom+1:])
}

func finish(header string, rows, trailing []string) ([]string, int, bool) {
	out := []string{header}
	omitted := 0
	if len(rows) > rowCap {
		omitted = len(rows) - rowHead - rowTail
		out = append(out, rows[:rowHead]...)
		out = append(out, fmt.Sprintf("…+%d clickhouse rows", omitted))
		out = append(out, rows[len(rows)-rowTail:]...)
	} else {
		out = append(out, rows...)
	}
	for _, t := range trailing {
		out = append(out, strings.TrimSpace(t))
	}
	return out, omitted, true
}

// names extracts the column names from a PrettyCompact top border such as
// "┌─id─┬─status───┬──────total─┐": each segment is dash-padded text.
func names(line string) string {
	line = strings.TrimLeft(line, " ")
	line = strings.TrimPrefix(line, "┌")
	line = strings.TrimSuffix(line, "┐")
	segs := strings.Split(line, "┬")
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = strings.Trim(s, "─")
	}
	return strings.Join(out, "|")
}

// rowCells turns "7. │  7 │ failed │" into "7|failed" cells joined by "|".
func rowCells(l string) string {
	i := strings.Index(l, "│")
	return cellsOf(l[i:], "│")
}

func cellsOf(l, sep string) string {
	l = strings.TrimSuffix(strings.TrimPrefix(l, sep), sep)
	parts := strings.Split(l, sep)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "|")
}

func splitLines(raw []byte) []string {
	text := strings.TrimRight(string(raw), "\r\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
