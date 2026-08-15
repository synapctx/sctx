package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

const statusEntriesPerSection = 10

func aggressiveStatus(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var out []string
	entries := 0
	flush := func() {
		if entries > statusEntriesPerSection {
			out = append(out, fmt.Sprintf("  …+%d more entries", entries-statusEntriesPerSection))
		}
		entries = 0
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			entries++
			if entries <= statusEntriesPerSection {
				out = append(out, line)
			}
			continue
		}
		flush()
		out = append(out, line)
	}
	flush()
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}
