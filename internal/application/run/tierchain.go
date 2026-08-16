package run

import (
	"bytes"
	"context"
	"fmt"

	"github.com/synapctx/sctx/internal/domain/format"
)

// RenderResult is the tier chain's outcome: the bytes to emit, which tier
// produced them, and the anomaly reason when a higher tier was rejected.
type RenderResult struct {
	Body       []byte
	Tier       format.Tier
	FoldStderr bool
	Elided     bool
	Note       string
	Anomaly    string
	// Fallback reports that the LOSSLESS fallback produced this render, not the
	// command's own formatter. The caller must account for it as generic
	// coverage: a dedicated formatter that declined did not cover the command,
	// and recording it as though it had is how the coverage meter goes blind.
	Fallback bool
}

// renderChain sequences tiers with one hard guarantee: an inapplicable tier,
// error, panic, or anomalous render degrades to the next tier and ultimately
// to verbatim — output is never suppressed. raw is the full captured stdout,
// used both as the verbatim fallback and for the anomaly guard.
//
// lossless is tried LAST, and only for a command whose own formatter produced
// nothing. Until this existed a dedicated formatter's decline was a DEAD END:
// the generic fallback was substituted only when NO formatter matched
// (service.Execute), so `mongosh` printing a JSON document, a `psql` shape the
// table parser does not recognise, or any of the 300-plus deliberate declines
// across the formatters went out at full size while an unmatched command
// printing the very same bytes was compacted. Measured on one developer's
// 30-day history: 27 of 31 mongosh runs verbatim at zero saved, and the same
// pattern on ssh, ls and git diff.
//
// WHY THE FALLBACK IS LOSSLESS-ONLY AND NOT THE GENERIC FORMATTER. Most of those
// declines are deliberate: a formatter steps aside so user-selected machine
// output — YAML, JSONPath, NUL-delimited records, --template — reaches the
// caller byte-authoritative. We have not enumerated which of the 338 decline
// sites mean "not my shape" and which mean "hands off", so the fallback must be
// safe without knowing. Whitespace-compacting a JSON document is: every consumer
// that parses it sees identical values. Collapsing repeated LINES is not — it
// rewrites a text stream a parser of an unknown format cannot survive — so the
// generic line collapser deliberately stays on the unmatched path only.
func renderChain(ctx context.Context, f format.Formatter, in format.Input, raw, rawStderr []byte, forceTier string, lossless format.Formatter) RenderResult {
	verbatim := RenderResult{Body: raw, Tier: format.TierVerbatim}

	if forceTier == string(format.TierVerbatim) || forceTier == "off" || f == nil {
		return verbatim
	}

	type tierFn struct {
		tier format.Tier
		fn   func(context.Context, format.Input) (format.Rendered, error)
	}
	tiers := []tierFn{
		{format.TierAggressive, f.Aggressive},
		{format.TierRelaxed, f.Relaxed},
	}
	if forceTier == string(format.TierRelaxed) {
		tiers = tiers[1:]
	}

	var anomaly string
	declined := 0
	for _, t := range tiers {
		// EACH TIER GETS ITS OWN READERS. Passing one Input to every tier meant the
		// first tier to READ stdout left the next one an empty stream — and reading
		// before deciding is the normal shape of a formatter, because you cannot
		// tell whether output is yours without looking at it. So any formatter whose
		// aggressive tier read and then declined had a DEAD relaxed tier, which the
		// chain then reported as "no tier handles this invocation".
		//
		// It hid behind unit tests because a test constructs a fresh Input per tier,
		// so the tier works in isolation and only fails in composition. Measured
		// consequence before the fix: `make` 167 runs at 0% saved with 101 of them
		// declining, and `ssh` declining 171 of 176.
		attempt := in
		attempt.Stdout = bytes.NewReader(raw)
		attempt.Stderr = bytes.NewReader(rawStderr)
		rendered, err := safeRender(ctx, t.fn, attempt)
		switch {
		case err == format.ErrTierInapplicable:
			// A tier saying "not mine" is a DESIGN DECISION, not a failure: `go test
			// -list` deliberately bypasses the test renderer so its answer survives.
			// Counted so the degradation log can tell it apart from a formatter that
			// broke — before this, both ended at verbatim with an empty anomaly and
			// the log listed working behaviour as a problem to investigate.
			declined++
			continue
		case err != nil:
			anomaly = appendAnomaly(anomaly, fmt.Sprintf("%s: %v", t.tier, err))
			continue
		}
		if reason := anomalous(rendered, in, raw); reason != "" {
			anomaly = appendAnomaly(anomaly, fmt.Sprintf("%s: %s", t.tier, reason))
			continue
		}
		return RenderResult{
			Body:       ensureTrailingNewline(rendered.Body),
			Tier:       t.tier,
			FoldStderr: rendered.FoldStderr,
			Elided:     rendered.Elided,
			Note:       rendered.Note,
			Anomaly:    anomaly,
		}
	}

	// The formatter that owns this command produced nothing usable. Before
	// falling all the way to verbatim, offer the bytes to the lossless
	// compactor; it detects and declines like every other tier, so the worst
	// case is exactly the verbatim that would have happened anyway.
	if lossless != nil {
		attempt := in
		attempt.Stdout = bytes.NewReader(raw)
		attempt.Stderr = bytes.NewReader(rawStderr)
		rendered, err := safeRender(ctx, lossless.Relaxed, attempt)
		if err == nil && anomalous(rendered, in, raw) == "" {
			return RenderResult{
				Body:       ensureTrailingNewline(rendered.Body),
				Tier:       format.TierRelaxed,
				FoldStderr: rendered.FoldStderr,
				Elided:     rendered.Elided,
				Note:       rendered.Note,
				Anomaly:    anomaly,
				Fallback:   true,
			}
		}
	}

	if anomaly == "" && declined > 0 {
		anomaly = DeclinedMarker
	}
	verbatim.Anomaly = anomaly
	return verbatim
}

// DeclinedMarker is recorded when every tier declined as inapplicable and nothing
// actually failed. It marks a deliberate bypass so the degradation log can separate
// "working as designed" from "could not compress" — a distinction the log existed to
// make and could not, because both cases arrived at verbatim with no anomaly at all.
const DeclinedMarker = "declined: no tier handles this invocation"

// safeRender converts a tier panic into an error so a formatter bug can
// never take down the wrapped command's output.
func safeRender(ctx context.Context, fn func(context.Context, format.Input) (format.Rendered, error), in format.Input) (rendered format.Rendered, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(ctx, in)
}

// anomalous rejects renders that plausibly hide information the agent needs:
// an empty body for non-empty raw output, or a "compression" at least as
// large as the raw output. Formatters must always emit at least a summary
// line — silence is never a valid compression.
func anomalous(r format.Rendered, in format.Input, raw []byte) string {
	if len(r.Body) == 0 && len(raw) > 0 {
		return "empty render for non-empty output"
	}
	if len(raw) > 0 && len(r.Body) >= len(raw) {
		return "render not smaller than raw output"
	}
	return ""
}

// ensureTrailingNewline terminates formatter renders cleanly. Verbatim
// output never passes through here — it must stay byte-exact.
func ensureTrailingNewline(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return b
	}
	return append(b, '\n')
}

func appendAnomaly(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "; " + add
}
