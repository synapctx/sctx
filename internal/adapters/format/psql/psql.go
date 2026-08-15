// Package psql bounds validated native psql result sets. It deliberately
// declines arbitrary unaligned output, command tags, failures, and shapes it
// cannot prove are complete tables or expanded records.
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

const (
	headRows = 5
	tailRows = 2
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
	return format.Rendered{}, format.ErrTierInapplicable
}

func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

func rendered(body string, omitted int, unit string) format.Rendered {
	return format.Rendered{
		Body:   []byte(body),
		Note:   fmt.Sprintf("psql: omitted %d %s", omitted, unit),
		Elided: true,
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
	}
	match := rowCountRE.FindStringSubmatch(lines[footer])
	if match == nil || !separator(lines[1]) {
		return "", 0, false
	}
	count, err := strconv.Atoi(match[1])
	if err != nil || count != footer-2 || count <= headRows+tailRows {
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

	omitted := count - headRows - tailRows
	out := append([]string(nil), lines[:2+headRows]...)
	out = append(out, fmt.Sprintf("…+%d psql rows", omitted))
	out = append(out, lines[footer-tailRows:footer]...)
	out = append(out, lines[footer])
	out = append(out, trailing...)
	return strings.Join(out, "\n"), omitted, true
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
			records = append(records, strings.Join(lines[start:i], "\n"))
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
	records = append(records, strings.Join(lines[start:], "\n"))
	if len(records) <= headRows+tailRows {
		return "", 0, false
	}
	omitted := len(records) - headRows - tailRows
	out := append([]string(nil), records[:headRows]...)
	out = append(out, fmt.Sprintf("…+%d psql records", omitted))
	out = append(out, records[len(records)-tailRows:]...)
	return strings.Join(out, "\n"), omitted, true
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
