package pytest

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// progressTrailerRE matches pytest's "[ 50%]" / "[100%]" progress trailer
// that closes out a per-file dot/letter progress row, or a -v per-test
// status row (e.g. "test_foo.py::test_bar PASSED [ 50%]").
var progressTrailerRE = regexp.MustCompile(`\[\s*\d{1,3}%\]$`)

// relaxedRender applies generic line-level filtering shared by any pytest
// invocation: it collapses runs of identical lines and drops obvious
// progress noise (the "test_foo.py .. F .." dot/letter progress row, blank
// lines), while retaining anything that looks like error signal (never
// dropping a line we don't recognize as noise).
func relaxedRender(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("pytest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("pytest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	outLines := relaxedFilter(string(stdout))
	errLines := relaxedFilter(string(stderr))

	var b strings.Builder
	for _, l := range outLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	foldStderr := false
	if len(errLines) > 0 {
		for _, l := range errLines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		foldStderr = true
	}

	body := b.String()
	if body == "" {
		body = fmt.Sprintf("(no diagnostic lines; %d bytes filtered)\n", len(stdout)+len(stderr))
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}, nil
}

// relaxedFilter collapses consecutive identical lines to "line ×N", drops
// blank lines, and retains anything mentioning error/failed/Error/assert
// or otherwise non-noisy content.
func relaxedFilter(text string) []string {
	lines := splitLines(text)
	if lines == nil {
		return nil
	}

	out := make([]string, 0, len(lines))

	i := 0
	for i < len(lines) {
		l := lines[i]
		trimmed := strings.TrimSpace(l)

		if trimmed == "" {
			i++
			continue
		}
		if isProgressLine(trimmed) {
			i++
			continue
		}

		j := i + 1
		for j < len(lines) && lines[j] == l {
			j++
		}
		n := j - i
		if n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", l, n))
		} else {
			out = append(out, l)
		}
		i = j
	}

	return out
}

// isProgressLine reports whether a trimmed line is pytest's per-file dot
// progress row or a -v per-test status row carrying a "[NN%]" trailer and
// a PASSED/SKIPPED/XFAIL status (no signal beyond what the final summary
// line already reports). Rows carrying FAILED/ERROR are kept: they are
// error signal even outside the "short test summary info" block.
func isProgressLine(trimmed string) bool {
	if !progressTrailerRE.MatchString(trimmed) {
		return false
	}
	if strings.Contains(trimmed, "FAILED") || strings.Contains(trimmed, "ERROR") {
		return false
	}
	return true
}
