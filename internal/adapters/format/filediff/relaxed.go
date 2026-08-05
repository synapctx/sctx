package filediff

import (
	"context"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Relaxed applies a generic blank/dupe line filter: drop blank lines and
// collapse runs of 2+ consecutive identical lines into one with a ×N
// marker. Never suppresses non-empty input.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("filediff: reading stdout: %w", err)
	}
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := splitLines(raw)
	out := filterRelaxedLines(lines)
	if len(out) == 0 {
		// Filtering removed everything (pure noise); never emit an empty
		// body for non-empty raw input, so fall back to the first line.
		if len(lines) > 0 {
			return format.Rendered{Body: []byte(lines[0])}, nil
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(strings.Join(out, "\n"))}, nil
}

// filterRelaxedLines drops blank lines and collapses runs of 2+ consecutive
// identical lines into one with a ×N marker.
func filterRelaxedLines(lines []string) []string {
	var out []string
	i := 0
	for i < len(lines) {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		count := j - i
		if count > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", lines[i], count))
		} else {
			out = append(out, lines[i])
		}
		i = j
	}
	return out
}
