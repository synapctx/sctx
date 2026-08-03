package kubectl

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// describeKeepPrefixes are field lines kept verbatim outside the Events
// section, matched against the trimmed line.
var describeKeepPrefixes = []string{"Name:", "Namespace:", "Status:", "Reason:", "Message:"}

// aggressiveDescribe keeps section header lines (non-indented), the
// Name/Namespace/Status/Reason/Message fields, and the entire Events
// section verbatim; everything else is elided with a per-gap …+N marker.
func aggressiveDescribe(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	inEvents := false
	var out []string
	elided := 0

	flushGap := func() {
		if elided > 0 {
			out = append(out, fmt.Sprintf("…+%d lines", elided))
			elided = 0
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isIndented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")

		if strings.HasPrefix(trimmed, "Events:") {
			inEvents = true
		}

		keep := inEvents
		if !keep && !isIndented && trimmed != "" {
			keep = true // section header line
		}
		if !keep {
			for _, p := range describeKeepPrefixes {
				if strings.HasPrefix(trimmed, p) {
					keep = true
					break
				}
			}
		}

		if keep {
			flushGap()
			out = append(out, line)
		} else {
			elided++
		}
	}
	flushGap()

	if len(out) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(out, "\n"))}, nil
}
