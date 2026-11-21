package mypy

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// filterRelaxedLines drops blank lines and dedupes consecutive duplicates.
func filterRelaxedLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// mypy prints nothing that is not signal: --pretty continuation and
		// snippet lines belong to their diagnostic, so every non-blank line is
		// kept. Only blank lines and consecutive duplicates drop.
		if len(out) > 0 && out[len(out)-1] == line {
			continue
		}
		out = append(out, line)
	}
	return out
}

// relaxedFilter is the mypy formatter's Relaxed tier: it applies to every
// invocation and never suppresses non-empty input.
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
