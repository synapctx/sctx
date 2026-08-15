package kubectl

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxTopRows caps large `kubectl top nodes|pods` tables. Native order is
// authoritative: callers can request --sort-by=cpu or --sort-by=memory, and
// sctx must not silently replace that choice with its own ordering.
const maxTopRows = 20

func aggressiveTop(in format.Input, _ []string) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	names, _ := parseHeader(lines[0])
	if colIndex(names, "NAME") < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rows := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			rows = append(rows, line)
		}
	}
	if len(rows) <= maxTopRows {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: renderCappedTable(lines[0], rows, maxTopRows)}, nil
}
