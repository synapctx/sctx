package kubectl

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxEventsRows caps the number of rows kept from `kubectl events` and
// `kubectl get events`.
const maxEventsRows = 20

// aggressiveEvents parses the events table (LAST SEEN/TYPE/REASON/OBJECT/
// MESSAGE), promoting Warning-type rows ahead of Normal ones before
// capping, so the most actionable events survive the cap.
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
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if typeIdx >= 0 {
			cols := splitColumns(line, starts)
			if typeIdx < len(cols) && cols[typeIdx] == "Warning" {
				warnings = append(warnings, line)
				continue
			}
		}
		others = append(others, line)
	}
	if len(warnings) == 0 && len(others) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	ordered := make([]string, 0, len(warnings)+len(others))
	ordered = append(ordered, warnings...)
	ordered = append(ordered, others...)

	body := renderCappedTable(lines[0], ordered, maxEventsRows)
	return format.Rendered{Body: body}, nil
}
