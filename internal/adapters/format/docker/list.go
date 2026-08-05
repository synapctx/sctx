package docker

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxListRows caps how many rows are kept from generic docker list/table
// subcommands (network ls, volume ls, history, top).
const maxListRows = 30

// createdByTruncateLen is the max length kept from `docker history`'s
// CREATED BY column before eliding the rest.
const createdByTruncateLen = 60

// aggressiveNetworkLs parses `docker network ls`'s table into one compact
// line per network: "name driver scope".
func aggressiveNetworkLs(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 networks")}, nil
	}

	names, starts := parseHeader(lines[0])
	nameIdx, driverIdx, scopeIdx := colIndex(names, "NAME"), colIndex(names, "DRIVER"), colIndex(names, "SCOPE")
	if colIndex(names, "NETWORK ID") < 0 || nameIdx < 0 || driverIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 networks")}, nil
	}

	capped, elided := capRows(rows, maxListRows)
	var b strings.Builder
	fmt.Fprintf(&b, "%d networks", len(rows))
	for _, cols := range capped {
		scope := ""
		if scopeIdx >= 0 {
			scope = cols[scopeIdx]
		}
		fmt.Fprintf(&b, "\n%s %s %s", cols[nameIdx], cols[driverIdx], scope)
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d more", elided)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}

// aggressiveVolumeLs parses `docker volume ls`'s table into one compact
// line per volume: "name driver".
func aggressiveVolumeLs(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 volumes")}, nil
	}

	names, starts := parseHeader(lines[0])
	driverIdx, nameIdx := colIndex(names, "DRIVER"), colIndex(names, "VOLUME NAME")
	if driverIdx < 0 || nameIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 volumes")}, nil
	}

	capped, elided := capRows(rows, maxListRows)
	var b strings.Builder
	fmt.Fprintf(&b, "%d volumes", len(rows))
	for _, cols := range capped {
		fmt.Fprintf(&b, "\n%s %s", cols[nameIdx], cols[driverIdx])
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d more", elided)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}

// aggressiveHistory parses `docker history`'s table into one compact line
// per image layer: "size created createdby", capping rows and truncating a
// long CREATED BY column.
func aggressiveHistory(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 layers")}, nil
	}

	names, starts := parseHeader(lines[0])
	createdIdx, createdByIdx, sizeIdx := colIndex(names, "CREATED"), colIndex(names, "CREATED BY"), colIndex(names, "SIZE")
	if colIndex(names, "IMAGE") < 0 || createdByIdx < 0 || sizeIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 layers")}, nil
	}

	capped, elided := capRows(rows, maxListRows)
	var b strings.Builder
	fmt.Fprintf(&b, "%d layers", len(rows))
	for _, cols := range capped {
		created := ""
		if createdIdx >= 0 {
			created = cols[createdIdx]
		}
		fmt.Fprintf(&b, "\n%s %s %s", cols[sizeIdx], created, truncate(cols[createdByIdx], createdByTruncateLen))
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d more", elided)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}

// aggressiveTop parses `docker top`'s process table into "pid cmd" lines,
// capping rows.
func aggressiveTop(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 processes")}, nil
	}

	names, starts := parseHeader(lines[0])
	pidIdx, cmdIdx := colIndex(names, "PID"), colIndex(names, "CMD")
	if pidIdx < 0 || cmdIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var rows [][]string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, splitColumns(line, starts))
	}
	if len(rows) == 0 {
		return format.Rendered{Body: []byte("0 processes")}, nil
	}

	capped, elided := capRows(rows, maxListRows)
	var b strings.Builder
	fmt.Fprintf(&b, "%d processes", len(rows))
	for _, cols := range capped {
		fmt.Fprintf(&b, "\n%s %s", cols[pidIdx], cols[cmdIdx])
	}
	if elided > 0 {
		fmt.Fprintf(&b, "\n…+%d more", elided)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}

// capRows returns at most max rows plus the number of rows dropped.
func capRows(rows [][]string, max int) (capped [][]string, elided int) {
	if len(rows) <= max {
		return rows, 0
	}
	return rows[:max], len(rows) - max
}

// truncate shortens s to at most n runes, appending an elision count when
// truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return fmt.Sprintf("%s…(+%d chars)", string(runes[:n]), len(runes)-n)
}
