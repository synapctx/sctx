package docker

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxStatsRows caps how many container rows are kept from `docker stats`.
const maxStatsRows = 20

// aggressiveStats parses `docker stats --no-stream`'s table into one
// compact line per container: "name cpu=X mem=Y(Z%)", capped at
// maxStatsRows. Without --no-stream the command streams repeated
// screen-clearing snapshots rather than a single table, so that case is
// left to Relaxed.
func aggressiveStats(in format.Input) (format.Rendered, error) {
	hasNoStream := false
	for _, a := range in.Argv {
		if a == "--no-stream" {
			hasNoStream = true
			break
		}
	}
	if !hasNoStream {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 containers")}, nil
	}

	names, starts := parseHeader(lines[0])
	nameIdx, cpuIdx, memIdx := colIndex(names, "NAME"), colIndex(names, "CPU %"), colIndex(names, "MEM USAGE / LIMIT")
	if colIndex(names, "CONTAINER ID") < 0 || nameIdx < 0 || cpuIdx < 0 || memIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	memPctIdx := colIndex(names, "MEM %")

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 containers")}, nil
	}

	capped := rows
	elided := 0
	if len(rows) > maxStatsRows {
		elided = len(rows) - maxStatsRows
		capped = rows[:maxStatsRows]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d containers", len(rows))
	for _, cols := range capped {
		memPct := ""
		if memPctIdx >= 0 {
			memPct = cols[memPctIdx]
		}
		fmt.Fprintf(&b, "\n%s cpu=%s mem=%s(%s)", cols[nameIdx], cols[cpuIdx], cols[memIdx], memPct)
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d more", elided)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}
