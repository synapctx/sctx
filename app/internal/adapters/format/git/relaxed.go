package git

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// filterRelaxedLines applies the generic relaxed-tier heuristics: drop
// hint/advice lines and progress lines, collapse runs of blank lines into a
// single blank, and always keep lines carrying error/warning/conflict
// content verbatim.
func filterRelaxedLines(lines []string) []string {
	var out []string
	prevBlank := false
	for _, line := range lines {
		if isHintOrAdviceLine(line) {
			continue
		}
		if isErrorLine(line) || strings.Contains(strings.ToLower(line), "warning:") {
			out = append(out, line)
			prevBlank = false
			continue
		}
		if isProgressLine(line) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, line)
	}
	// Trim a leading/trailing blank line produced by collapsing.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// relaxedFilter is the git formatter's Relaxed tier: it applies to every git
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
		// Filtering removed everything (pure noise); never emit an empty
		// body for non-empty raw input, so fall back to a minimal summary.
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
