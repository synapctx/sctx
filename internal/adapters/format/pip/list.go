package pip

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxListRows caps how many "pkg==version" rows are kept verbatim from
// `pip list`/`pip freeze` before eliding the rest behind a "…+N more" marker.
const maxListRows = 60

// aggressiveList renders `pip list` (a two-column table) or `pip freeze`/
// `pip list --format=freeze` (bare "pkg==version" lines) into a capped,
// one-row-per-package body. The `[notice] A new release of pip...` footer
// pip prints is dropped.
func aggressiveList(in format.Input, freeze bool) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)

	var rows []string
	if freeze {
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "[notice]") {
				continue
			}
			rows = append(rows, t)
		}
	} else {
		if len(lines) < 2 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		names, starts := parseHeader(lines[0])
		pkgIdx, verIdx := colIndex(names, "Package"), colIndex(names, "Version")
		if pkgIdx < 0 || verIdx < 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		dataLines := lines[1:]
		if len(dataLines) > 0 && strings.Trim(dataLines[0], "- ") == "" {
			dataLines = dataLines[1:]
		}
		for _, l := range dataLines {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "[notice]") {
				continue
			}
			cols := splitColumns(l, starts)
			if pkgIdx >= len(cols) || verIdx >= len(cols) {
				continue
			}
			rows = append(rows, fmt.Sprintf("%s==%s", cols[pkgIdx], cols[verIdx]))
		}
	}

	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 packages")}, nil
	}

	shown, extra := rows, 0
	if len(rows) > maxListRows {
		shown, extra = rows[:maxListRows], len(rows)-maxListRows
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d packages", len(rows))
	for _, r := range shown {
		b.WriteString("\n")
		b.WriteString(r)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "\n…+%d more", extra)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}

// parseHeader splits a pip table header into column names and their
// starting byte offset in the header line. Columns are separated by runs of
// two or more spaces, so a single embedded space (e.g. "Editable project
// location") is preserved as part of the name.
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
