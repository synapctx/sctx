// Package sed implements a format.Formatter for the narrow, provably-safe
// sed invocations that are actually a READ: `sed -n 'A,Bp' FILE` and
// `sed -n '/re/p' FILE`. sed alone was the single largest coverage-gap
// entry (1,454 events, per the 2026-09-04 meter) because sed's general
// output is whatever its script transforms — arbitrary text chosen by the
// expression, not a coherent shape a formatter can parse. But these two
// shapes print an UNTRANSFORMED subset of the file's own lines with `-n`
// suppressing sed's normal auto-print, which is exactly what cat already
// renders safely, so this delegates wholesale to the `read` package (cat's
// internals) rather than reimplementing its JSON/JSONL sniffing and
// dedupe rules. Any other sed invocation — a substitution, multiple files,
// `-i`, a script file, an unrecognised address form — declines both tiers,
// so the generic fallback or verbatim tier handles it; this stops the
// coverage-gap churn on the leading program without pretending to parse
// sed's general script language.
package sed

import (
	"context"

	"github.com/synapctx/sctx/internal/adapters/format/read"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/sedargv"
)

// Formatter renders the two recognised sed read-shapes.
type Formatter struct {
	inner format.Formatter
}

// New constructs a sed Formatter.
func New() *Formatter { return &Formatter{inner: read.New()} }

// Descriptor claims all sed invocations; recognizedRead decides which ones
// this formatter actually has something to do with.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "sed"}
}

// Aggressive delegates a recognised read-shape to the read package; every
// other sed invocation declines. The recognition grammar lives in
// platform/sedargv, shared with the hook's wrap decision so the two never
// disagree about which shapes are safe.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !sedargv.RecognizedRead(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return f.inner.Aggressive(ctx, in)
}

// Relaxed mirrors Aggressive, delegating to read's relaxed tier.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !sedargv.RecognizedRead(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return f.inner.Relaxed(ctx, in)
}
