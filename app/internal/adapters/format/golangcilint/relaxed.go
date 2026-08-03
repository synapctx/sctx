package golangcilint

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// isRuntimeNoiseLine reports whether line is one of golangci-lint's own
// structured logging lines (e.g. "level=info msg=..."), which carry no
// signal for an agent and are safe to drop unconditionally.
func isRuntimeNoiseLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "level=info")
}

// filterRelaxedLines collapses consecutive blank lines and consecutive
// duplicate lines, and drops golangci-lint's runtime-info noise; it is the
// generic fallback for any invocation not handled by the Aggressive tier.
func filterRelaxedLines(lines []string) []string {
	var out []string
	prevBlank := false
	for _, line := range lines {
		if isRuntimeNoiseLine(line) {
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
		if len(out) > 0 && out[len(out)-1] == line {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// relaxedFilter is the golangci-lint formatter's Relaxed tier: it applies to
// every invocation and never suppresses non-empty input.
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
