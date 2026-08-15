package kubectl

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxNormalEventRows caps routine event rows. Warning rows are never capped:
// they are the error signal this formatter exists to preserve.
const maxNormalEventRows = 20

// aggressiveEvents parses the events table (LAST SEEN/TYPE/REASON/OBJECT/
// MESSAGE), promoting every Warning-type row ahead of Normal ones and only
// capping the Normal tail.
func aggressiveEvents(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "No resources found") {
		return format.Rendered{Body: []byte(lines[0])}, nil
	}
	if len(lines) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	names, starts := parseHeader(lines[0])
	typeIdx := colIndex(names, "TYPE")

	var warnings, others []string
	reordered := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if typeIdx >= 0 {
			cols := splitColumns(line, starts)
			if typeIdx < len(cols) && cols[typeIdx] == "Warning" {
				if len(others) > 0 {
					reordered = true
				}
				warnings = append(warnings, line)
				continue
			}
		}
		others = append(others, line)
	}
	if len(warnings) == 0 && len(others) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	shownNormal := others
	elidedNormal := 0
	if len(shownNormal) > maxNormalEventRows {
		elidedNormal = len(shownNormal) - maxNormalEventRows
		shownNormal = shownNormal[:maxNormalEventRows]
	}
	if !reordered && elidedNormal == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	b.WriteString(lines[0])
	for _, line := range warnings {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	for _, line := range shownNormal {
		b.WriteByte('\n')
		b.WriteString(line)
	}
	if elidedNormal > 0 {
		fmt.Fprintf(&b, "\n…+%d Normal rows", elidedNormal)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}
