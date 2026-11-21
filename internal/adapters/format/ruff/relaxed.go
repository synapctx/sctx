package ruff

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// relaxedLines renders ruff output without interpreting it: each diagnostic
// block collapses to "file:line:col: CODE message" (its source/diff snippet
// is the only thing dropped, with a count) and every other line is kept,
// with consecutive duplicates removed. Nothing a reader needs to locate or
// understand a diagnostic is lost.
func relaxedLines(lines []string) ([]string, bool) {
	var out []string
	dropped := false
	for _, it := range parseItems(lines) {
		var l string
		switch {
		case it.isBlock:
			fix := ""
			if it.fixable {
				fix = " [*]"
			}
			l = fmt.Sprintf("%s:%s:%s: %s%s %s", it.file, it.line, it.col, it.code, fix, it.msg)
			if it.dropped > 1 {
				l += fmt.Sprintf(" …+%d context", it.dropped)
				dropped = true
			}
		case strings.TrimSpace(it.plain) == "":
			continue
		default:
			l = it.plain
		}
		if len(out) > 0 && out[len(out)-1] == l {
			continue
		}
		out = append(out, l)
	}
	return out, dropped
}

// relaxedFilter is the ruff formatter's Relaxed tier: it applies to every
// invocation and never suppresses non-empty input.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)

	if userShapedOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdoutLines, d1 := relaxedLines(splitLines(rawStdout))
	stderrLines, d2 := relaxedLines(splitLines(rawStderr))

	if len(stdoutLines) == 0 && len(stderrLines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var body []string
	body = append(body, stdoutLines...)
	body = append(body, stderrLines...)

	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(stderrLines) > 0,
		Elided:     d1 || d2,
	}, nil
}
