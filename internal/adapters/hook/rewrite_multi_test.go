package hook

import "testing"

// TestEveryEligibleSegmentIsWrapped covers the savings this used to leave behind:
// wrapping only the first eligible segment meant `ls && go build ./...` compressed the
// `ls` and left the build — the output-heavy half — untouched, purely because the cheap
// command came first.
func TestEveryEligibleSegmentIsWrapped(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"cheap command first no longer costs the expensive one",
			"ls && go build ./...", "sctx ls && sctx go build ./..."},
		{"three segments", "git status ; ls ; go vet ./...",
			"sctx git status ; sctx ls ; sctx go vet ./..."},
		// A segment RECEIVING piped input is still skipped: it is not a pipeline head,
		// and wrapping it would compress an already-compressed stream.
		{"pipeline consumer is not wrapped", "go test ./... | tail -50",
			"sctx go test ./... | tail -50"},
		// A segment with an OUTPUT redirect is still skipped while its neighbours are
		// wrapped — the per-segment guards keep applying independently.
		{"redirected segment skipped, neighbour still wrapped",
			"go build ./... > out.txt && go vet ./...",
			"go build ./... > out.txt && sctx go vet ./..."},
		// An unknown program between two known ones must not break either.
		{"unknown program between known ones", "git status && cargo build && go vet ./...",
			"sctx git status && cargo build && sctx go vet ./..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewrite(tc.in)
			if !ok {
				t.Fatalf("rewrite(%q) declined", tc.in)
			}
			if got != tc.want {
				t.Errorf("rewrite(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAlreadyWrappedStillDeclinesEntirely — the guard against double-wrapping must not
// weaken now that several insertions happen per command.
func TestAlreadyWrappedStillDeclinesEntirely(t *testing.T) {
	for _, in := range []string{
		"sctx go test ./... && go vet ./...",
		"git status && sctx go test ./...",
	} {
		if got, ok := rewrite(in); ok {
			t.Errorf("rewrite(%q) = %q, want declined — a partially wrapped chain must not be re-wrapped", in, got)
		}
	}
}
