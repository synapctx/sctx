package ruff

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// worthKeeping reports whether a relaxed-tier line carries signal: a
// diagnostic/per-file line ("path:line:col:" or "Reformatted"/"Would
// reformat:" shape) or anything mentioning an error, as opposed to blank
// lines or progress noise.
func worthKeeping(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if diagnosticRe.MatchString(line) || perFileRe.MatchString(line) {
		return true
	}
	if foundRe.MatchString(line) || fixableHintRe.MatchString(line) || formatSummaryRe.MatchString(line) {
		return true
	}
	if strings.Contains(line, "error") || strings.Contains(line, "Error") {
		return true
	}
	return trimmed == "All checks passed!"
}

// filterRelaxedLines drops blank/progress noise, deduplicates consecutive
// identical lines, and retains anything with diagnostic or error shape.
func filterRelaxedLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !worthKeeping(line) {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == line {
			continue
		}
		out = append(out, line)
	}
	return out
}

// relaxedFilter is the ruff formatter's Relaxed tier: it applies to every
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
