package awscli

import (
	"strings"
)

// isBorder reports a table rule line: only + - | and no padding spaces, which a
// content row always has.
func isBorder(l string) bool {
	if l == "" || strings.ContainsAny(l, " \t") {
		return false
	}
	return strings.Trim(l, "+-|") == ""
}

// isTable recognises `--output table`: a rule of dashes first, then a boxed row.
func isTable(text string) bool {
	lines := strings.SplitN(text, "\n", 3)
	if len(lines) < 2 {
		return false
	}
	return len(lines[0]) >= 10 && strings.Trim(lines[0], "-") == "" && strings.HasPrefix(lines[1], "|")
}

type tableWriter struct {
	out   []string
	title string
	depth int
	pairs []string
	have  bool
}

func (t *tableWriter) flush() {
	if !t.have {
		return
	}
	ind := strings.Repeat("  ", max(0, t.depth-2))
	if len(t.pairs) > 0 {
		t.out = append(t.out, ind+t.title+": "+strings.Join(t.pairs, "; "))
	} else {
		t.out = append(t.out, ind+t.title)
	}
	t.have, t.pairs = false, nil
}

// splitRow strips the leading and trailing |-runs (their length is the nesting
// depth) and returns the trimmed cells.
func splitRow(l string) (int, []string) {
	lead := len(l) - len(strings.TrimLeft(l, "|"))
	trail := len(l) - len(strings.TrimRight(l, "|"))
	d := min(lead, trail)
	if d == 0 {
		return 0, nil
	}
	inner := l[d : len(l)-d]
	cells := strings.Split(inner, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return d, cells
}

// renderTable turns the aws CLI table box into text. Nested sections (depth >= 2)
// of two-cell rows are key/value records and are joined into one line per
// section; every other row keeps one line with " | " between cells. Only rules
// and padding are dropped.
func renderTable(text string) string {
	t := &tableWriter{}
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		l = strings.TrimRight(l, "\r")
		if isBorder(l) {
			continue
		}
		if !strings.HasPrefix(l, "|") {
			// Not table text: a stray line keeps its place verbatim.
			t.flush()
			t.out = append(t.out, l)
			continue
		}
		d, cells := splitRow(l)
		switch {
		case d == 0:
			t.flush()
			t.out = append(t.out, l)
		case len(cells) == 1:
			t.flush()
			t.title, t.depth, t.have = cells[0], d, true
		case d >= 2 && len(cells) == 2 && t.have:
			t.pairs = append(t.pairs, cells[0]+"="+cells[1])
		default:
			t.flush()
			t.out = append(t.out, strings.Repeat("  ", max(0, d-2))+strings.Join(cells, " | "))
		}
	}
	t.flush()
	return strings.Join(t.out, "\n")
}
