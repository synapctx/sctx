package brew

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// filterRelaxedLines drops blank lines, download progress bars, and
// ==> Fetching/Downloading spam, and collapses runs of consecutive
// identical lines into a single line with a ×N marker. Error/Warning
// lines, ==> Caveats, and 🍺 result lines are ordinary lines here and pass
// through untouched (never noise-matched, so never dropped).
func filterRelaxedLines(lines []string) []string {
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}
		if progressBarRe.MatchString(trimmed) {
			i++
			continue
		}
		if strings.HasPrefix(line, "==> Fetching") || strings.HasPrefix(line, "==> Downloading") {
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

// relaxedFilter is the brew formatter's Relaxed tier: it applies to every
// brew invocation and never suppresses non-empty input.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)

	if len(rawStdout) == 0 && len(rawStderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	stdoutLines := filterRelaxedLines(splitLines(rawStdout))
	stderrLines := filterRelaxedLines(splitLines(rawStderr))

	if len(stdoutLines) == 0 && len(stderrLines) == 0 {
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
