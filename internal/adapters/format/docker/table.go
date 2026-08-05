package docker

import "strings"

// parseHeader splits a docker table header into column names and their
// starting byte offset in the header line. Columns are separated by runs of
// two or more spaces, so a single embedded space (e.g. "CONTAINER ID") is
// preserved as part of the name.
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
// parseHeader, trimming each field. Values may contain spaces (e.g. PORTS),
// which is why offset-based slicing is used instead of field-splitting.
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
