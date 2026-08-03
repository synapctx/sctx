package npm

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveRun strips npm/yarn's own script-invocation wrapper lines
// (e.g. "> pkg@1.0.0 test" / "> jest ./test") from run/test/exec output,
// keeping the underlying script's stdout/stderr — including failures —
// intact. If nothing recognizable was stripped, the tier declines so the
// relaxed/verbatim tiers render the (unpredictable) script output as-is
// rather than risk over-compressing a test failure.
func aggressiveRun(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	if len(rawOut) == 0 && len(rawErr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	outLines, outStripped := stripWrapperLines(splitLines(rawOut))
	errLines, errStripped := stripWrapperLines(splitLines(rawErr))
	if outStripped == 0 && errStripped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var body []string
	body = append(body, outLines...)
	body = append(body, errLines...)

	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(errLines) > 0,
	}, nil
}

// stripWrapperLines removes npm/yarn's script-invocation echo lines,
// returning the remaining lines and how many were removed.
func stripWrapperLines(lines []string) (kept []string, stripped int) {
	for _, l := range lines {
		if isWrapperLine(strings.TrimSpace(l)) {
			stripped++
			continue
		}
		kept = append(kept, l)
	}
	return kept, stripped
}
