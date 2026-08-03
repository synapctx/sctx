package kubectl

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// isSeparatorLine reports whether trimmed is a pure separator line made
// entirely of dash/equals/underscore characters.
func isSeparatorLine(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r != '-' && r != '=' && r != '_' {
			return false
		}
	}
	return true
}

// filterRelaxedLines drops empty and pure-separator lines and collapses
// runs of consecutive identical lines into a single line with a ×N marker.
func filterRelaxedLines(lines []string) []string {
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isSeparatorLine(trimmed) {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j] == line {
			j++
		}
		count := j - i
		if count > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", line, count))
		} else {
			out = append(out, line)
		}
		i = j
	}
	return out
}

// relaxedFilter is the kubectl formatter's Relaxed tier: it applies to
// every kubectl subcommand and never suppresses non-empty input.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)

	if len(rawOut) == 0 && len(rawErr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	outLines := filterRelaxedLines(splitLines(rawOut))
	errLines := filterRelaxedLines(splitLines(rawErr))

	if len(outLines) == 0 && len(errLines) == 0 {
		// Filtering removed everything (pure noise); never emit an empty
		// body for non-empty raw input, so fall back to a minimal summary.
		summary := firstNonEmptyLine(rawOut)
		if summary == "" {
			summary = firstNonEmptyLine(rawErr)
		}
		if summary == "" {
			summary = "(no output)"
		}
		return format.Rendered{Body: []byte(summary), FoldStderr: len(rawErr) > 0}, nil
	}

	var body []string
	body = append(body, outLines...)
	body = append(body, errLines...)

	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(errLines) > 0,
	}, nil
}

func firstNonEmptyLine(raw []byte) string {
	for _, l := range splitLines(raw) {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}
