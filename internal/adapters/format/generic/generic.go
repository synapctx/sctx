// Package generic is the fallback formatter for commands no dedicated
// formatter claims. It is what makes coverage a gradient rather than a cliff.
//
// THE MEASUREMENT THAT MOTIVATED IT. Over 34 days and 11,621 runs, commands with
// no formatter saved ZERO tokens — 179 runs, 50,124 raw tokens, nothing
// recovered — because the fallback sniffed JSON and did nothing else. Anything
// that printed repetitive TEXT paid full price. Meanwhile SCTX.md promised
// readers that "JSON stdout is compacted automatically and repeated lines are
// collapsed, whatever the program". The first half was true; the second was not.
// This package makes the sentence true rather than deleting it.
//
// WHY A GENERIC FORMATTER IS SAFE HERE, when the repository's rule is that
// formatters are written against output captured from the real binary. That rule
// exists because a parser that ASSUMES a shape can misread it — a table parser
// pointed at output it has never seen drops columns and no one notices. Nothing
// here assumes a shape. Both tiers DETECT and then decline:
//
//   - aggressive compacts only what parses as JSON, or bounds a stream whose
//     every nonblank line independently parses as JSON;
//   - relaxed collapses only runs of lines that are byte-identical, or identical
//     once a narrow leading-timestamp pattern is stripped, and always prints the
//     count it collapsed.
//
// Neither can silently lose content, so neither needs a fixture per tool. That
// is the whole argument for pointing this at `aws`, `gcloud`, `terraform` and
// every command nobody has written a formatter for yet: the tier chain can only
// improve on verbatim or fall back to it.
package generic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/adapters/format/jsonlines"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter composes the JSON content-sniffer with generic line-run collapsing.
type Formatter struct {
	json *jsoncompact.Formatter
}

// New constructs the generic fallback.
func New() *Formatter { return &Formatter{json: jsoncompact.New()} }

// Descriptor returns a label-only Match. "(generic)" is never matched against a
// real argv[0]; the run service passes this formatter explicitly when nothing
// else claimed the command.
//
// It is deliberately DISTINCT from a dedicated formatter's name so the accounting
// can still answer "which commands are running with no real coverage?" — the
// question the whole coverage-gap meter exists to answer. The previous fallback
// recorded itself as "(json)" and reported FormatterMatched=true, which quietly
// counted a sniffed command as covered.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "(generic)"}
}

// Aggressive compacts a single JSON document or bounds a valid JSONL/NDJSON
// stream. Failed commands keep their complete record stream so diagnostics are
// never discarded merely because each line happens to be valid JSON.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("generic: reading stdout: %w", err)
	}

	jsonIn := in
	jsonIn.Stdout = bytes.NewReader(raw)
	if rendered, err := f.json.Aggressive(ctx, jsonIn); err == nil {
		return rendered, nil
	} else if err != format.ErrTierInapplicable {
		return format.Rendered{}, err
	}

	if in.ExitCode == 0 {
		if rendered, ok := jsonlines.Render(raw); ok {
			return rendered, nil
		}
	}
	return format.Rendered{}, format.ErrTierInapplicable
}

// Relaxed compacts JSON that survived the aggressive tier's own guards, and
// otherwise collapses repeated line runs.
//
// The JSON attempt comes first and its inapplicability is what routes non-JSON
// to the collapser: a JSON document is a single logical line as far as run
// collapsing is concerned, so trying the collapser on it would find nothing and
// waste the tier. Note the input must be re-read for the second attempt, since
// the first consumed the reader.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("generic: reading stdout: %w", err)
	}

	jsonIn := in
	jsonIn.Stdout = bytes.NewReader(raw)
	if rendered, err := f.json.Relaxed(ctx, jsonIn); err == nil {
		return rendered, nil
	} else if err != format.ErrTierInapplicable {
		return format.Rendered{}, err
	}

	classification := jsonlines.Classify(raw)
	if classification == jsonlines.MixedJSONLines ||
		(classification == jsonlines.ValidJSONLines && in.ExitCode != 0) {
		return format.Rendered{}, format.ErrTierInapplicable
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

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
