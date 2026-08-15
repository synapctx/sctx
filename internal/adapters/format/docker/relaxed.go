package docker

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// filterRelaxedLines preserves every unique line and collapses only exact
// consecutive runs of at least three lines. Blank lines and separators are
// part of native diagnostics and remain authoritative.
func filterRelaxedLines(lines []string) []string {
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		j := i + 1
		for j < len(lines) && lines[j] == line {
			j++
		}
		count := j - i
		if count >= 3 {
			out = append(out, fmt.Sprintf("%s ×%d", line, count))
		} else {
			for k := 0; k < count; k++ {
				out = append(out, line)
			}
		}
		i = j
	}
	return out
}

// relaxedFilter is the docker formatter's Relaxed tier: it applies to every
// docker subcommand and never suppresses non-empty input.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)

	if len(rawOut) == 0 && len(rawErr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	outLines := filterRelaxedLines(splitLines(rawOut))
	errLines := filterRelaxedLines(splitLines(rawErr))

	var body []string
	body = append(body, outLines...)
	body = append(body, errLines...)

	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(errLines) > 0,
	}, nil
}
