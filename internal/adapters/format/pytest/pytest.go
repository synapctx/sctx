// Package pytest formats output from the `pytest` CLI into a token-minimal
// rendering. It claims every `pytest` invocation; pytest has no first-arg
// subcommand to dispatch on, unlike gotest's `go test`/`go build`/`go vet`.
package pytest

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `pytest` output for the aggressive and relaxed tiers.
// It is safe for concurrent use; it holds no mutable state.
type Formatter struct{}

// New constructs a Formatter for the `pytest` CLI.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all `pytest` invocations.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "pytest"}
}

// Aggressive renders a structured, maximally compressed summary of a pytest
// run: the final result line, the FAILED/ERROR node ids, and a short
// excerpt per failure. It returns format.ErrTierInapplicable when the
// output doesn't look like pytest at all.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return aggressiveRender(in)
}

// Relaxed applies generic line-level noise filtering: collapsing repeated
// lines, dropping progress noise, and retaining anything that looks like
// error signal.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return relaxedRender(in)
}

// readAll drains r fully, tolerating a nil reader (treated as empty).
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// splitLines splits text on newlines without leaving a trailing empty
// element for a final "\n".
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
