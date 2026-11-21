// Package mysqlcli compacts the output of the `mysql` command-line client run
// non-interactively (`mysql -e`, `mysql < file`). A result set is the answer
// the agent asked for, so nothing is dropped below a high threshold: -t box
// tables lose only their borders and padding, -E vertical records lose only
// the padding, and tab-separated batch output (the default when stdout is not
// a terminal) is already lossless. Above rowCap rows (recordCap -E records)
// the middle is replaced by an explicit "…+N mysql rows" marker. The
// "Using a password on the command line" notice on stderr is the one piece of
// stderr noise it removes; every other stderr line is kept.
package mysqlcli

import (
	"context"
	"fmt"
	"io"
	"strings"

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

const passwordWarning = "mysql: [Warning] Using a password on the command line interface can be insecure."

// Formatter renders `mysql` output.
type Formatter struct{}

// New constructs a mysql Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims the `mysql` program; there is no subcommand.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "mysql"} }

// Aggressive compacts a successful run. A failed run is left to the relaxed
// tier so error text is never reshaped.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readBoth(in)
	if err != nil {
		return format.Rendered{}, err
	}
	body, omitted, unit, ok := compact(stdout)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	keptErr := stripNotice(stderr)
	if keptErr != "" {
		body += keptErr
	}
	if len(body) >= len(stdout)+len(stderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:       []byte(body),
		Note:       fmt.Sprintf("mysql: omitted %d %s", omitted, unit),
		FoldStderr: true,
		Elided:     omitted > 0,
	}, nil
}

// Relaxed only drops the password notice from stderr, on success or failure,
// and re-emits stdout and every other stderr line untouched.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr, err := readBoth(in)
	if err != nil {
		return format.Rendered{}, err
	}
	keptErr := stripNotice(stderr)
	if len(keptErr) >= len(stderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := string(stdout)
	if body != "" && !strings.HasSuffix(body, "\n") && keptErr != "" {
		body += "\n"
	}
	body += keptErr
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true}, nil
}

// stripNotice removes the password notice line and returns the rest of stderr.
func stripNotice(stderr []byte) string {
	if len(stderr) == 0 {
		return ""
	}
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(string(stderr), "\n"), "\n") {
		if strings.TrimSpace(l) == passwordWarning {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func readBoth(in format.Input) ([]byte, []byte, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return nil, nil, fmt.Errorf("mysql: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return nil, nil, fmt.Errorf("mysql: reading stderr: %w", err)
	}
	return stdout, stderr, nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// compact dispatches on the shape of stdout. It reports ok=false unless the
// whole text is provably one of the known result shapes.
func compact(stdout []byte) (body string, omitted int, unit string, ok bool) {
	lines := splitLines(stdout)
	if len(lines) == 0 {
		return "", 0, "", false
	}
	switch {
	case isBorder(lines[0]) || strings.HasPrefix(lines[0], "Warning (Code") || strings.HasPrefix(lines[0], "Note (Code"):
		body, omitted, ok = tables(lines)
		return body, omitted, "rows", ok
	case isRecordHeader(lines[0]):
		body, omitted, ok = records(lines)
		return body, omitted, "records", ok
	case strings.Contains(lines[0], "\t"):
		body, omitted, ok = batch(lines)
		return body, omitted, "rows", ok
	}
	return "", 0, "", false
}

// batch caps tab-separated output (one row per line; mysql escapes embedded
// newlines in this mode). Below the cap it is already lossless: decline.
func batch(lines []string) (string, int, bool) {
	if len(lines) <= rowCap {
		return "", 0, false
	}
	omitted := len(lines) - rowHead - rowTail
	out := append([]string(nil), lines[:rowHead]...)
	out = append(out, fmt.Sprintf("…+%d mysql rows", omitted))
	out = append(out, lines[len(lines)-rowTail:]...)
	return strings.Join(out, "\n") + "\n", omitted, true
}

// tables compacts one or more consecutive `+---+` box tables. Diagnostic
// lines ("Warning (Code N): ...") before a table are kept verbatim.
func tables(lines []string) (string, int, bool) {
	var out []string
	total := 0
	for i := 0; i < len(lines); {
		l := lines[i]
		if !isBorder(l) {
			if !strings.HasPrefix(l, "Warning (Code") && !strings.HasPrefix(l, "Note (Code") && !strings.HasPrefix(l, "Error (Code") {
				return "", 0, false
			}
			out = append(out, l)
			i++
			continue
		}
		// border, header, border, rows..., border
		if i+2 >= len(lines) || !isRow(lines[i+1]) || !isBorder(lines[i+2]) {
			return "", 0, false
		}
		pipes := strings.Count(lines[i+1], "|")
		j := i + 3
		var rows []string
		for j < len(lines) && !isBorder(lines[j]) {
			if !isRow(lines[j]) || strings.Count(lines[j], "|") != pipes {
				return "", 0, false
			}
			rows = append(rows, lines[j])
			j++
		}
		if j >= len(lines) || len(lines[j]) != len(lines[i]) {
			return "", 0, false
		}
		out = append(out, cells(lines[i+1]))
		if len(rows) > rowCap {
			for _, r := range rows[:rowHead] {
				out = append(out, cells(r))
			}
			out = append(out, fmt.Sprintf("…+%d mysql rows", len(rows)-rowHead-rowTail))
			total += len(rows) - rowHead - rowTail
			rows = rows[len(rows)-rowTail:]
		}
		for _, r := range rows {
			out = append(out, cells(r))
		}
		i = j + 1
	}
	return strings.Join(out, "\n") + "\n", total, true
}

// cells strips the outer pipes and the padding around every cell.
func cells(line string) string {
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "|")
}

func isBorder(l string) bool {
	if len(l) < 3 || l[0] != '+' || l[len(l)-1] != '+' {
		return false
	}
	for _, r := range l {
		if r != '+' && r != '-' {
			return false
		}
	}
	return true
}

func isRow(l string) bool {
	return len(l) >= 2 && l[0] == '|' && l[len(l)-1] == '|'
}

const recordRule = "***************************"

func isRecordHeader(l string) bool {
	return strings.HasPrefix(l, recordRule+" ") && strings.HasSuffix(l, ". row "+recordRule)
}

// records compacts -E (vertical) output. Every non-header line of a record
// must carry its colon in the same column (the name width); a value that
// spans lines breaks that and the whole output is declined, so no continuation
// line is ever re-attached to the wrong field.
func records(lines []string) (string, int, bool) {
	var recs [][]string
	for _, l := range lines {
		if isRecordHeader(l) {
			recs = append(recs, []string{l})
			continue
		}
		recs[len(recs)-1] = append(recs[len(recs)-1], l)
	}
	var texts []string
	for _, rec := range recs {
		text, ok := compactRecord(rec)
		if !ok {
			return "", 0, false
		}
		texts = append(texts, text)
	}
	omitted := 0
	if len(texts) > recordCap {
		omitted = len(texts) - recordHead - recordTail
		kept := append([]string(nil), texts[:recordHead]...)
		kept = append(kept, fmt.Sprintf("…+%d mysql records", omitted))
		texts = append(kept, texts[len(texts)-recordTail:]...)
	}
	return strings.Join(texts, "\n") + "\n", omitted, true
}

func compactRecord(rec []string) (string, bool) {
	header := strings.TrimSuffix(strings.TrimPrefix(rec[0], recordRule+" "), " "+recordRule)
	out := []string{"*** " + header + " ***"}
	width := -1
	for _, l := range rec[1:] {
		idx := strings.Index(l, ":")
		if idx < 0 || (idx+1 < len(l) && l[idx+1] != ' ') {
			return "", false
		}
		if width < 0 {
			width = idx
		}
		if idx != width {
			return "", false
		}
		out = append(out, strings.TrimLeft(l[:idx], " ")+l[idx:])
	}
	return strings.Join(out, "\n"), true
}

func splitLines(raw []byte) []string {
	text := strings.TrimRight(string(raw), "\r\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
