package npm

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// isRelaxedNoiseLine reports whether l is progress-spinner/funding noise
// that carries no signal once collapsed. Error/warning lines are never
// noise-matched so they always survive verbatim.
func isRelaxedNoiseLine(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return false
	}
	if isFundingLine(t) {
		return true
	}
	switch {
	case strings.HasPrefix(t, "npm WARN") && !strings.Contains(strings.ToLower(t), "deprecat"):
		return true
	case strings.HasPrefix(t, "npm warn") && !strings.Contains(strings.ToLower(t), "deprecat"):
		return true
	case strings.HasPrefix(t, "Progress:"):
		return true
	case strings.HasPrefix(t, "+ "), strings.HasPrefix(t, "- "):
		return true
	default:
		return false
	}
}

// capDeprecationLines caps deprecation-warning lines within an already
// noise-filtered line slice, since they can be as voluminous as any other
// progress noise on a large install.
func capDeprecationLines(lines []string) []string {
	var out []string
	deprecated := 0
	extra := 0
	for _, l := range lines {
		if isDeprecationLine(l) {
			deprecated++
			if deprecated > maxDeprecationWarnings {
				extra++
				continue
			}
		}
		out = append(out, l)
	}
	if extra > 0 {
		out = append(out, fmt.Sprintf("…+%d more deprecation warnings", extra))
	}
	return out
}

// filterRelaxedLines drops progress/funding/warning noise and blank lines,
// caps deprecation warnings, and collapses non-consecutive duplicate lines
// into one occurrence annotated with an "×N" marker.
func filterRelaxedLines(lines []string) []string {
	type entry struct {
		line  string
		count int
	}
	var order []entry
	seen := map[string]int{}
	dropped := 0

	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if isRelaxedNoiseLine(l) {
			dropped++
			continue
		}
		if idx, ok := seen[l]; ok {
			order[idx].count++
			continue
		}
		seen[l] = len(order)
		order = append(order, entry{line: l, count: 1})
	}

	deduped := make([]string, 0, len(order))
	for _, e := range order {
		if e.count > 1 {
			deduped = append(deduped, fmt.Sprintf("%s  ×%d", e.line, e.count))
			continue
		}
		deduped = append(deduped, e.line)
	}

	out := capDeprecationLines(deduped)
	if dropped > 0 {
		out = append(out, fmt.Sprintf("…+%d noise lines", dropped))
	}
	return out
}

// relaxedFilter is the npm/pnpm/yarn formatter's Relaxed tier: it applies
// to every subcommand and never suppresses non-empty input.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)

	stdoutLines := filterRelaxedLines(splitLines(rawStdout))
	stderrLines := filterRelaxedLines(splitLines(rawStderr))

	if len(stdoutLines) == 0 && len(stderrLines) == 0 {
		if len(rawStdout) == 0 && len(rawStderr) == 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		summary := firstNonEmptyLine(rawStdout)
		if summary == "" {
			summary = firstNonEmptyLine(rawStderr)
		}
		if summary == "" {
			summary = "(no output)"
		}
		return format.Rendered{Body: []byte(summary), FoldStderr: len(rawStderr) > 0}, nil
	}

	var body []string
	body = append(body, stdoutLines...)
	body = append(body, stderrLines...)

	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(stderrLines) > 0,
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
