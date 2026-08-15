package kubectl

import (
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveLogs collapses only consecutive byte-identical log lines, with
// an exact ×N marker. Arbitrary head/tail truncation is intentionally avoided:
// a unique error can occur anywhere in a log and must never be hidden merely
// because the stream is long.
func aggressiveLogs(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	outLines, outChanged := collapse.Runs(collapse.SplitLines(rawOut))
	errLines, errChanged := collapse.Runs(collapse.SplitLines(rawErr))
	if !outChanged && !errChanged {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append([]string(nil), outLines...)
	body = append(body, errLines...)
	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(rawErr) > 0,
	}, nil
}
