package makefmt

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// dirLineRe matches GNU make's recursive-invocation directory markers, e.g.
// "make[1]: Entering directory '/repo/sub'" or the older
// "make: Leaving directory `/repo/sub'" form.
var dirLineRe = regexp.MustCompile("^make(?:\\[\\d+\\])?: (?:Entering|Leaving) directory ['`\"](.*)['\"]$")

// errorSignalKeywords are case-insensitive substrings that mark a line as
// carrying error/warning content, which must never be collapsed or dropped.
var errorSignalKeywords = []string{"error", "warning", "fail", "undefined", "cannot", "no rule"}

func isErrorSignalLine(line string) bool {
	lower := strings.ToLower(line)
	for _, kw := range errorSignalKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func dirLineMatch(line string) (path string, ok bool) {
	m := dirLineRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// aggressiveMake classifies make output line by line:
//   - runs of consecutive Entering/Leaving directory markers collapse into a
//     single "dir: <path> ×N" marker (path is the last directory in the run);
//   - lines carrying error/warning signal always pass through verbatim, never
//     collapsed even if repeated;
//   - any other run of 2+ identical consecutive lines collapses into the
//     line plus a "×N" marker (covers both repeated recipe echoes and
//     generic noise repetition);
//   - everything else passes through unchanged.
//
// Nothing is ever silently erased: every collapse carries its ×N count, so
// the same logic is safe to apply regardless of exit code and never
// compresses away the error signal on a failing build.
func aggressiveMake(in format.Input) (format.Rendered, error) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)
	lines := append(splitLines(rawStdout), splitLines(rawStderr)...)

	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var out []string
	transforms := 0

	i := 0
	for i < len(lines) {
		line := lines[i]

		if path, ok := dirLineMatch(line); ok {
			j := i
			count := 0
			lastPath := path
			for j < len(lines) {
				p, ok2 := dirLineMatch(lines[j])
				if !ok2 {
					break
				}
				lastPath = p
				count++
				j++
			}
			if count > 1 {
				out = append(out, fmt.Sprintf("dir: %s ×%d", lastPath, count))
				transforms++
			} else {
				out = append(out, lines[i])
			}
			i = j
			continue
		}

		if isErrorSignalLine(line) {
			out = append(out, line)
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
			transforms++
		} else {
			out = append(out, line)
		}
		i = j
	}

	if transforms == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{
		Body:       []byte(strings.Join(out, "\n")),
		FoldStderr: len(rawStderr) > 0,
	}, nil
}
