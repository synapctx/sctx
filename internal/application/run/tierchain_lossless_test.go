package run

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/domain/telemetry"
)

// prettyJSON is what a covered command prints when it hands back a document its
// own formatter has no grammar for — mongosh printing a record, psql in a mode
// the table parser declines, a CLI's `--format json`. It is 40% whitespace.
const prettyJSON = `{
  "items": [
    {
      "name": "first",
      "state": "ready"
    },
    {
      "name": "second",
      "state": "ready"
    }
  ]
}
`

func declining() *fakeFormatter {
	inapplicable := func(format.Input) (format.Rendered, error) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return &fakeFormatter{
		match:      format.Match{Command: "tool"},
		aggressive: inapplicable,
		relaxed:    inapplicable,
	}
}

// THE DEAD END THIS CLOSES. A command with a dedicated formatter that declines
// every tier used to go out at full size, while an UNMATCHED command printing
// the identical bytes was compacted — the generic fallback was substituted only
// when nothing matched at all.
func TestADeclinedCommandStillReachesTheLosslessCompactor(t *testing.T) {
	raw := []byte(prettyJSON)
	got := renderChain(context.Background(), declining(), format.Input{Argv: []string{"tool"}},
		raw, nil, "", jsoncompact.New())

	if !got.Fallback {
		t.Fatal("the fallback did not run; a declined formatter is still a dead end")
	}
	if len(got.Body) >= len(raw) {
		t.Errorf("no saving: %d >= %d bytes", len(got.Body), len(raw))
	}
	// LOSSLESS. Not "smaller" — the same document, and every value still present.
	var before, after any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.Body, &after); err != nil {
		t.Fatalf("the fallback emitted something that no longer parses as JSON: %v", err)
	}
	if a, b := mustMarshal(t, before), mustMarshal(t, after); a != b {
		t.Errorf("the document changed:\n before %s\n after  %s", a, b)
	}
}

// The fallback is LAST. A formatter that can render its own command must never
// be pre-empted by it.
func TestTheLosslessFallbackNeverPreemptsAWorkingFormatter(t *testing.T) {
	f := declining()
	f.aggressive = func(format.Input) (format.Rendered, error) {
		return format.Rendered{Body: []byte("2 items ready")}, nil
	}
	got := renderChain(context.Background(), f, format.Input{Argv: []string{"tool"}},
		[]byte(prettyJSON), nil, "", jsoncompact.New())

	if got.Fallback {
		t.Error("the fallback ran although the command's own formatter rendered it")
	}
	if string(got.Body) != "2 items ready\n" {
		t.Errorf("body = %q, want the formatter's own render", got.Body)
	}
}

// Output the fallback cannot prove anything about is left exactly as it is —
// the guarantee that lets it run after 300-plus deliberate declines without
// knowing which of them meant "leave this alone".
func TestOutputTheFallbackCannotProveIsLeftVerbatim(t *testing.T) {
	raw := []byte("total 8\ndrwxr-xr-x  4 user staff  128 Aug 16 10:02 internal\n")
	got := renderChain(context.Background(), declining(), format.Input{Argv: []string{"tool"}},
		raw, nil, "", jsoncompact.New())

	if got.Fallback {
		t.Error("the fallback claimed non-JSON output")
	}
	if got.Tier != format.TierVerbatim || !bytes.Equal(got.Body, raw) {
		t.Errorf("tier = %s, body = %q; want untouched verbatim", got.Tier, got.Body)
	}
}

// ACCOUNTING. The saving is real but it is NOT coverage: the command's own
// formatter declined. Recording it as dedicated would tell the coverage meter
// that mongosh, psql and every other decline are handled — and the meter is what
// decides which formatter gets written next.
func TestAFallbackSavingIsAccountedAsGenericNotCoverage(t *testing.T) {
	registry := NewRegistry()
	registry.Register(declining())
	emitter := &fakeEmitter{}
	stdout := &bytes.Buffer{}
	svc := NewService(registry, fakeRunner{stdout: prettyJSON}, nil, emitter, nil,
		stdout, &bytes.Buffer{},
		Options{Version: "test", LosslessFallback: jsoncompact.New()})

	if _, err := svc.Execute(context.Background(), []string{"tool", "show"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(emitter.events))
	}
	ev := emitter.events[0]
	if ev.FormatterKind != telemetry.FormatterKindGeneric {
		t.Errorf("formatterKind = %q, want %q: a declined formatter did not cover this command",
			ev.FormatterKind, telemetry.FormatterKindGeneric)
	}
	if ev.FormatterMatched {
		t.Error("formatterMatched is true although the dedicated formatter declined")
	}
	if !ev.OutputReduced || ev.SavedTokens <= 0 {
		t.Errorf("no saving recorded: reduced=%t saved=%d", ev.OutputReduced, ev.SavedTokens)
	}
	if strings.Contains(stdout.String(), "\n  ") {
		t.Errorf("output was not compacted: %q", stdout.String())
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
