package kubectl

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxRolloutHistoryRows caps the number of revision rows kept from
// `kubectl rollout history`.
const maxRolloutHistoryRows = 20

// aggressiveRollout dispatches `kubectl rollout` on its sub-subcommand
// (status/history); rest is the argv following "rollout".
func aggressiveRollout(in format.Input, rest []string) (format.Rendered, error) {
	if len(rest) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	switch rest[0] {
	case "status":
		return aggressiveRolloutStatus(in)
	case "history":
		return aggressiveRolloutHistory(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// collapseRepeatedLines collapses runs of consecutive identical raw lines
// into a single line with a trailing ×N marker.
func collapseRepeatedLines(lines []string) []string {
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		j := i + 1
		for j < len(lines) && lines[j] == line {
			j++
		}
		if count := j - i; count > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", line, count))
		} else {
			out = append(out, line)
		}
		i = j
	}
	return out
}

// aggressiveRolloutStatus collapses repeated progress lines (e.g. "Waiting
// for deployment ... rollout to finish") with a ×N marker, keeping the
// final line (typically "... successfully rolled out") verbatim.
func aggressiveRolloutStatus(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out := collapseRepeatedLines(lines)
	if len(out) == len(lines) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(out, "\n"))}, nil
}

// aggressiveRolloutHistory caps the REVISION table kept from `kubectl
// rollout history`, keeping any preamble (the resource name line) verbatim.
func aggressiveRolloutHistory(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	headerIdx := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "REVISION") {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for _, l := range lines[:headerIdx] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.Write(renderCappedTable(lines[headerIdx], lines[headerIdx+1:], maxRolloutHistoryRows))
	return format.Rendered{Body: []byte(b.String())}, nil
}
