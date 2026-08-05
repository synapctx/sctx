package kubectl

import (
	"fmt"
	"strings"
)

// parseHeader splits a kubectl table header into column names and their
// starting byte offset in the header line. Columns are separated by runs of
// two or more spaces; kubectl column names never contain embedded spaces.
func parseHeader(header string) (names []string, starts []int) {
	i := 0
	for i < len(header) {
		for i < len(header) && header[i] == ' ' {
			i++
		}
		if i >= len(header) {
			break
		}
		start := i
		for i < len(header) {
			if header[i] == ' ' && (i+1 >= len(header) || header[i+1] == ' ') {
				break
			}
			i++
		}
		names = append(names, strings.TrimSpace(header[start:i]))
		starts = append(starts, start)
	}
	return names, starts
}

// colIndex returns the index of want in names, or -1 if absent.
func colIndex(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

// splitColumns slices a data row using the column start offsets computed by
// parseHeader, trimming each field.
func splitColumns(line string, starts []int) []string {
	cols := make([]string, len(starts))
	for i := range starts {
		if starts[i] > len(line) {
			continue
		}
		end := len(line)
		if i+1 < len(starts) && starts[i+1] <= len(line) {
			end = starts[i+1]
		}
		cols[i] = strings.TrimSpace(line[starts[i]:end])
	}
	return cols
}

// renderCappedTable renders a table header line followed by up to maxRows
// pre-formatted data rows, eliding any remainder with an explicit
// "…+N rows" marker. Used by subcommands where capping row count (rather
// than kubectl get's healthy/unhealthy grouping) is the right compression.
func renderCappedTable(header string, rows []string, maxRows int) []byte {
	var b strings.Builder
	b.WriteString(header)
	shown := rows
	elided := 0
	if len(shown) > maxRows {
		elided = len(shown) - maxRows
		shown = shown[:maxRows]
	}
	for _, r := range shown {
		if strings.TrimSpace(r) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(r)
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d rows", elided)
	}
	return []byte(b.String())
}
