// Package psql compacts validated native psql result sets. A SELECT result is
// the answer the agent asked for, so rows are never dropped below a high
// threshold: aligned tables lose only column padding and the dashed rule, and
// expanded records lose only the name padding and rule tail. Above rowCap rows
// (recordCap expanded records, lineCap tuples-only/CSV lines) the middle is
// replaced by an explicit "…+N psql rows" marker. It deliberately declines
// command tags, failures, and shapes it cannot prove are complete tables or
// expanded records.
package psql

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Result-set caps. Nothing is omitted at or below the cap; above it the first
// *Head and last *Tail entries are kept and the remainder is counted.
const (
	rowCap     = 200
	rowHead    = 150
	rowTail    = 50
	recordCap  = 100
	recordHead = 70
	recordTail = 30
	lineCap    = 200
	lineHead   = 150
	lineTail   = 50
)

var (
	rowCountRE = regexp.MustCompile(`^\(([0-9]+) rows?\)$`)
	recordRE   = regexp.MustCompile(`^-\[ RECORD ([0-9]+) \]-+$`)
)

type Formatter struct{}

func New() *Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "psql"} }

func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("psql: reading stdout: %w", err)
	}
	if out, omitted, ok := aligned(raw); ok && len(out) < len(raw) {
		return rendered(out, omitted, "rows"), nil
	}
	if out, omitted, ok := expanded(raw); ok && len(out) < len(raw) {
		return rendered(out, omitted, "records"), nil
	}
	if delimitedMode(in.Argv) {
		if out, omitted, ok := delimited(raw); ok && len(out) < len(raw) {
			return rendered(out, omitted, "lines"), nil
		}
	}
	return format.Rendered{}, format.ErrTierInapplicable
}

func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

func rendered(body string, omitted int, unit string) format.Rendered {
	return format.Rendered{
		Body:   []byte(body),
		Note:   fmt.Sprintf("psql: omitted %d %s", omitted, unit),
		Elided: omitted > 0,
	}
}

func aligned(raw []byte) (string, int, bool) {
	lines := splitLines(raw)
	if len(lines) < 4 {
		return "", 0, false
	}
	footer := len(lines) - 1
	trailing := []string(nil)
	if strings.HasPrefix(lines[footer], "Time: ") {
		trailing = []string{lines[footer]}
		footer--
		if footer > 0 && lines[footer] == "" {
			footer--
		}
	}
	match := rowCountRE.FindStringSubmatch(lines[footer])
	if match == nil || !separator(lines[1]) {
		return "", 0, false
	}
	count, err := strconv.Atoi(match[1])
	if err != nil || count != footer-2 {
		return "", 0, false
	}
	pipes := strings.Count(lines[0], "|")
	if strings.Count(lines[1], "+") != pipes {
		return "", 0, false
	}
	for _, line := range lines[2:footer] {
		if strings.Count(line, "|") != pipes {
			return "", 0, false
		}
	}

	rows := lines[2:footer]
	omitted := 0
	out := []string{unpad(lines[0])}
	if count > rowCap {
		omitted = count - rowHead - rowTail
		out = appendUnpadded(out, rows[:rowHead])
		out = append(out, fmt.Sprintf("…+%d psql rows", omitted))
		out = appendUnpadded(out, rows[count-rowTail:])
	} else {
		out = appendUnpadded(out, rows)
	}
	out = append(out, lines[footer])
	out = append(out, trailing...)
	return strings.Join(out, "\n"), omitted, true
}

// unpad removes the alignment padding around every cell of one aligned row.
// Cell values keep their text; only their surrounding spaces are lost.
func unpad(line string) string {
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return strings.Join(cells, "|")
}

func appendUnpadded(out, rows []string) []string {
	for _, r := range rows {
		out = append(out, unpad(r))
	}
	return out
}

// delimited bounds -t/-A/--csv output, where completeness cannot be proven
// from a footer, by lines. It only runs when argv selected such a mode.
func delimited(raw []byte) (string, int, bool) {
	lines := splitLines(raw)
	if len(lines) <= lineCap {
		return "", 0, false
	}
	omitted := len(lines) - lineHead - lineTail
	out := append([]string(nil), lines[:lineHead]...)
	out = append(out, fmt.Sprintf("…+%d psql lines", omitted))
	out = append(out, lines[len(lines)-lineTail:]...)
	return strings.Join(out, "\n"), omitted, true
}

// delimitedMode reports whether argv asks for unaligned tuples-only or CSV
// output (-t, -A, -tA, --tuples-only, --no-align, --csv, -P format=...).
func delimitedMode(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		switch {
		case a == "--tuples-only", a == "--no-align", a == "--csv":
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1 && strings.Trim(a[1:], "tA") == "":
			return true
		}
	}
	return false
}

func expanded(raw []byte) (string, int, bool) {
	lines := splitLines(raw)
	if len(lines) == 0 {
		return "", 0, false
	}
	var records []string
	start := -1
	for i, line := range lines {
		match := recordRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if start >= 0 {
			records = append(records, compactRecord(lines[start:i]))
		} else if i != 0 {
			return "", 0, false
		}
		n, err := strconv.Atoi(match[1])
		if err != nil || n != len(records)+1 {
			return "", 0, false
		}
		start = i
	}
	if start < 0 {
		return "", 0, false
	}
	records = append(records, compactRecord(lines[start:]))
	omitted := 0
	if len(records) > recordCap {
		omitted = len(records) - recordHead - recordTail
		kept := append([]string(nil), records[:recordHead]...)
		kept = append(kept, fmt.Sprintf("…+%d psql records", omitted))
		records = append(kept, records[len(records)-recordTail:]...)
	}
	return strings.Join(records, "\n"), omitted, true
}

// compactRecord rewrites one expanded record: the rule after the header is cut
// to "-[ RECORD n ]" and each "name   | value" row loses the name padding.
func compactRecord(lines []string) string {
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if i == 0 {
			out = append(out, strings.SplitAfter(l, "]")[0])
			continue
		}
		name, value, ok := strings.Cut(l, "|")
		if !ok {
			out = append(out, l)
			continue
		}
		out = append(out, strings.TrimRight(name, " ")+"|"+strings.TrimPrefix(value, " "))
	}
	return strings.Join(out, "\n")
}

func separator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(trimmed, "-") {
		return false
	}
	for _, r := range trimmed {
		if r != '-' && r != '+' {
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
