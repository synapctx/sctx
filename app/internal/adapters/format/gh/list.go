package gh

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxTitleLen is the truncation width applied to list-view titles.
const maxTitleLen = 70

// isEmptyListMessage reports whether line is one of gh's "nothing here"
// sentences for pr/issue list, which are already minimal and should pass
// through verbatim rather than being (mis)parsed as a table row.
func isEmptyListMessage(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.Contains(lower, "no pull requests match") ||
		strings.Contains(lower, "no issues match") ||
		strings.Contains(lower, "there are no open")
}

// aggressiveList renders `gh pr list` / `gh issue list` TSV output into one
// compact line per row: "#<num> <status> <title truncated>", preceded by a
// count summary line.
func aggressiveList(in format.Input, kind string) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	// A single non-tabular sentence (empty-list message) is already minimal.
	if len(lines) == 1 && !strings.Contains(lines[0], "\t") {
		return format.Rendered{Body: []byte(lines[0])}, nil
	}

	var rows []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if isEmptyListMessage(line) {
			return format.Rendered{Body: []byte(line)}, nil
		}

		var cols []string
		if strings.Contains(line, "\t") {
			cols = strings.Split(line, "\t")
		} else {
			cols = strings.Fields(line)
		}
		if len(cols) == 0 {
			continue
		}

		num := strings.TrimSpace(cols[0])
		if _, err := strconv.Atoi(strings.TrimPrefix(num, "#")); err != nil {
			// Not a data row (e.g. a header); skip it.
			continue
		}
		if !strings.HasPrefix(num, "#") {
			num = "#" + num
		}

		title := ""
		if len(cols) > 1 {
			title = strings.TrimSpace(cols[1])
		}
		if len(title) > maxTitleLen {
			title = title[:maxTitleLen-1] + "…"
		}

		status := ""
		if len(cols) > 2 {
			status = strings.TrimSpace(cols[len(cols)-1])
		}

		row := num
		if status != "" {
			row += " " + status
		}
		row += " " + title
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d %s\n", len(rows), kind)
	b.WriteString(strings.Join(rows, "\n"))

	return format.Rendered{Body: []byte(b.String())}, nil
}
