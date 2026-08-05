package mypy

import (
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// locLineRe matches any line carrying a "file:line:" location prefix, mypy's
// generic diagnostic shape regardless of severity.
var locLineRe = regexp.MustCompile(`:\d+:`)

// keepRelaxedLine reports whether a line carries diagnostic signal worth
// retaining: a "file:line:" location, or an explicit error/note marker.
func keepRelaxedLine(line string) bool {
	if locLineRe.MatchString(line) {
		return true
	}
	return strings.Contains(line, "error:") || strings.Contains(line, "note:") ||
		strings.HasPrefix(line, "Found ") || strings.HasPrefix(line, "Success:")
}

// filterRelaxedLines drops blank/progress noise, keeps only lines carrying
// diagnostic signal, and dedupes consecutive duplicates.
func filterRelaxedLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !keepRelaxedLine(line) {
			continue
		}
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
