package run

import (
	"context"
	"errors"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

type fakeFormatter struct {
	match      format.Match
	aggressive func(format.Input) (format.Rendered, error)
	relaxed    func(format.Input) (format.Rendered, error)
}

func (f *fakeFormatter) Descriptor() format.Match { return f.match }
func (f *fakeFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.aggressive(in)
}
func (f *fakeFormatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.relaxed(in)
}

func TestRenderChain(t *testing.T) {
	raw := []byte("line one\nline two\nline three\n")
	ok := func(body string) func(format.Input) (format.Rendered, error) {
		return func(format.Input) (format.Rendered, error) {
			return format.Rendered{Body: []byte(body)}, nil
		}
	}
	fail := func(err error) func(format.Input) (format.Rendered, error) {
		return func(format.Input) (format.Rendered, error) { return format.Rendered{}, err }
	}

	tests := []struct {
		name        string
		f           format.Formatter
		forceTier   string
		wantTier    format.Tier
		wantBody    string
		wantAnomaly bool
	}{
		{
			name:     "aggressive wins",
			f:        &fakeFormatter{aggressive: ok("compact"), relaxed: ok("relaxed")},
			wantTier: format.TierAggressive,
			wantBody: "compact\n",
		},
		{
			name:     "inapplicable aggressive degrades silently",
			f:        &fakeFormatter{aggressive: fail(format.ErrTierInapplicable), relaxed: ok("relaxed")},
			wantTier: format.TierRelaxed,
			wantBody: "relaxed\n",
		},
		{
			name:        "erroring aggressive records anomaly",
			f:           &fakeFormatter{aggressive: fail(errors.New("parse boom")), relaxed: ok("relaxed")},
			wantTier:    format.TierRelaxed,
			wantBody:    "relaxed\n",
			wantAnomaly: true,
		},
		{
			name: "panicking tier degrades to verbatim",
			f: &fakeFormatter{
				aggressive: func(format.Input) (format.Rendered, error) { panic("boom") },
				relaxed:    func(format.Input) (format.Rendered, error) { panic("boom") },
			},
			wantTier:    format.TierVerbatim,
			wantBody:    string(raw),
			wantAnomaly: true,
		},
		{
			name:        "empty render rejected as anomaly",
			f:           &fakeFormatter{aggressive: ok(""), relaxed: ok("relaxed")},
			wantTier:    format.TierRelaxed,
			wantBody:    "relaxed\n",
			wantAnomaly: true,
		},
		{
			name:        "render not smaller than raw rejected",
			f:           &fakeFormatter{aggressive: ok(string(raw) + "extra"), relaxed: ok("relaxed")},
			wantTier:    format.TierRelaxed,
			wantBody:    "relaxed\n",
			wantAnomaly: true,
		},
		{
			name:      "force verbatim bypasses formatter",
			f:         &fakeFormatter{aggressive: ok("compact"), relaxed: ok("relaxed")},
			forceTier: "verbatim",
			wantTier:  format.TierVerbatim,
			wantBody:  string(raw),
		},
		{
			name:      "force relaxed skips aggressive",
			f:         &fakeFormatter{aggressive: ok("compact"), relaxed: ok("relaxed")},
			forceTier: "relaxed",
			wantTier:  format.TierRelaxed,
			wantBody:  "relaxed\n",
		},
		{
			name:     "nil formatter is verbatim",
			f:        nil,
			wantTier: format.TierVerbatim,
			wantBody: string(raw),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderChain(context.Background(), tt.f, format.Input{ExitCode: 0}, raw, nil, tt.forceTier, nil)
			if got.Tier != tt.wantTier {
				t.Fatalf("tier = %q, want %q (anomaly: %q)", got.Tier, tt.wantTier, got.Anomaly)
			}
			if string(got.Body) != tt.wantBody {
				t.Fatalf("body = %q, want %q", got.Body, tt.wantBody)
			}
			if tt.wantAnomaly && got.Anomaly == "" {
				t.Fatal("expected an anomaly to be recorded")
			}
			if !tt.wantAnomaly && got.Anomaly != "" {
				t.Fatalf("unexpected anomaly: %q", got.Anomaly)
			}
		})
	}
}

func TestRenderChainPreservesElisionSignal(t *testing.T) {
	raw := []byte("one\ntwo\nthree\n")
	f := &fakeFormatter{
		aggressive: func(format.Input) (format.Rendered, error) {
			return format.Rendered{Body: []byte("summary"), Elided: true}, nil
		},
		relaxed: func(format.Input) (format.Rendered, error) {
			return format.Rendered{}, format.ErrTierInapplicable
		},
	}
	result := renderChain(context.Background(), f, format.Input{}, raw, nil, "", nil)
	if !result.Elided {
		t.Fatal("tier chain dropped formatter elision signal")
	}
}

// A render that folds stderr into its body replaces both streams, so it is
// measured against stdout plus stderr. A body larger than stdout alone but
// smaller than both streams together (findings on stdout, the summary on
// stderr) used to be rejected as "not smaller than raw output".
func TestRenderChainMeasuresAFoldedStderrRenderAgainstBothStreams(t *testing.T) {
	stdout := []byte("finding one\nfinding two\n")
	stderr := []byte("summary: lots of banner and progress text that the render drops\n")
	body := "finding one\nfinding two\nsummary: 2 findings" // > len(stdout), < both streams
	if len(body) <= len(stdout) || len(body) >= len(stdout)+len(stderr) {
		t.Fatalf("fixture sizes are wrong: body %d, stdout %d, stderr %d", len(body), len(stdout), len(stderr))
	}
	render := func(fold bool) *fakeFormatter {
		fn := func(format.Input) (format.Rendered, error) {
			return format.Rendered{Body: []byte(body), FoldStderr: fold}, nil
		}
		return &fakeFormatter{aggressive: fn, relaxed: fn}
	}
	got := renderChain(context.Background(), render(true), format.Input{}, stdout, stderr, "", nil)
	if got.Tier != format.TierAggressive || got.Anomaly != "" {
		t.Fatalf("folded-stderr render: tier %q anomaly %q, want aggressive with none", got.Tier, got.Anomaly)
	}
	// Without FoldStderr the stderr is still emitted beside the body, so the
	// comparison stays against stdout alone.
	got = renderChain(context.Background(), render(false), format.Input{}, stdout, stderr, "", nil)
	if got.Tier != format.TierVerbatim {
		t.Fatalf("unfolded render larger than stdout: tier %q, want verbatim", got.Tier)
	}
}
