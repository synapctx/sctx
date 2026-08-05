package pip

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// progressBarRe matches pip's percentage-prefixed progress bar lines, e.g.
// "45%|████████                | 1.2/2.7 MB 3.1 MB/s eta 0:00:01".
var progressBarRe = regexp.MustCompile(`^\s*\d+%\|`)

// isNoiseLine reports whether l is pip progress-bar or footer noise that
// carries no signal once collapsed.
func isNoiseLine(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return false
	}
	if strings.Contains(t, "[notice]") {
		return true
	}
	if progressBarRe.MatchString(t) {
		return true
	}
	if strings.Contains(t, "━") {
		return true
	}
	return false
}

// filterRelaxedLines drops progress-bar/[notice] noise and blank lines, and
// collapses non-consecutive duplicate lines (esp. repeated "Requirement
// already satisfied: ..." lines across a large install) into one occurrence
// annotated with an "×N" marker. ERROR/WARNING lines are never noise-matched
// so they always survive verbatim.
func filterRelaxedLines(lines []string) []string {
	type entry struct {
		line  string
		count int
	}
	var order []entry
	seen := map[string]int{} // line -> index into order
	dropped := 0

	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if isNoiseLine(l) {
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

	out := make([]string, 0, len(order)+1)
	for _, e := range order {
		if e.count > 1 {
			out = append(out, fmt.Sprintf("%s  ×%d", e.line, e.count))
			continue
		}
		out = append(out, e.line)
	}
	if dropped > 0 {
		out = append(out, fmt.Sprintf("…+%d noise lines", dropped))
	}
	return out
}

// relaxedFilter is the pip formatter's Relaxed tier: it applies to every
// subcommand and never suppresses non-empty input.
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
