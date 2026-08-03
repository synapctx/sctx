package kubectl

import (
	"sort"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxTopRows caps the number of rows kept from `kubectl top nodes|pods`.
const maxTopRows = 20

// aggressiveTop parses the `kubectl top` table (NAME/CPU(cores)/MEMORY...),
// sorting rows by CPU descending so the heaviest consumers survive the cap.
func aggressiveTop(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	names, starts := parseHeader(lines[0])
	cpuIdx := -1
	for i, n := range names {
		if strings.HasPrefix(n, "CPU") {
			cpuIdx = i
			break
		}
	}

	var rows []string
	var cpus []int
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, line)
		cpu := 0
		if cpuIdx >= 0 {
			cols := splitColumns(line, starts)
			if cpuIdx < len(cols) {
				cpu = parseCPUMilli(cols[cpuIdx])
			}
		}
		cpus = append(cpus, cpu)
	}
	if len(rows) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return cpus[idx[a]] > cpus[idx[b]] })
	sorted := make([]string, len(rows))
	for i, ix := range idx {
		sorted[i] = rows[ix]
	}

	body := renderCappedTable(lines[0], sorted, maxTopRows)
	return format.Rendered{Body: body}, nil
}

// parseCPUMilli parses a kubectl top CPU value ("123m" millicores, or a
// bare core count) into millicores for sorting purposes. Unparseable
// values sort last (0).
func parseCPUMilli(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "m") {
		n, _ := strconv.Atoi(strings.TrimSuffix(s, "m"))
		return n
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int(f * 1000)
}
