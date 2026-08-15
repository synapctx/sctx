package read

import (
	"context"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Relaxed collapses runs of 3+ blank lines to one blank plus an explicit
// "…+N blank" marker, runs of 3+ consecutive identical lines to one line plus a
// "×N" marker, and runs of 3+ consecutive log lines that are identical once a
// leading timestamp is stripped to one representative line (with its own
// timestamp) plus a "×N" marker. If no run met a threshold, the tier is
// inapplicable so the chain tries the next tier instead of re-emitting the input
// unchanged.
//
// The rules themselves live in adapters/format/collapse, because the generic
// fallback for commands with no dedicated formatter needs exactly these and no
// others — see that package's doc comment for why only provable redundancy is
// safe to compress without a captured fixture.
func (f *formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("read: reading stdout: %w", err)
	}
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	out, changed := collapse.Runs(collapse.SplitLines(raw))
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	body := strings.Join(out, "\n")
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Elided: true}, nil
}
