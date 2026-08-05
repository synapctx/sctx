package gotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// TestInspectionModesAreNotSummarised is the worst class of bug this formatter can
// have: not compressing badly, but DISCARDING the answer. Measured before the fix,
// `go test -list 'Test.*' ./internal/adapters/hook/` printed six test names and
// rendered as "ok: 1 packages, 444ms" — an agent listing tests would conclude there
// are none.
func TestInspectionModesAreNotSummarised(t *testing.T) {
	listOutput := "TestRunClaudeNoFallback\nTestParseFallback\nok  \tgithub.com/x/y\t0.4s\n"

	for _, argv := range [][]string{
		{"go", "test", "-list", "Test.*", "./..."},
		{"go", "test", "-list=Test.*", "./..."},
		{"go", "test", "-n", "./..."},
		{"go", "test", "-h"},
	} {
		in := format.Input{
			Argv:    argv,
			Command: "go test",
			Stdout:  strings.NewReader(listOutput),
		}
		f := New()
		if _, err := f.Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: aggressive tier must decline, got err=%v", argv, err)
		}
		// Relaxed must decline too, or the answer is still filtered rather than passed
		// through — every line of a listing is signal.
		if _, err := f.Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: relaxed tier must decline, got err=%v", argv, err)
		}
	}
}

// TestOrdinaryTestRunsAreStillFormatted — the fix must not disable compression for the
// case that matters most. A flag merely CONTAINING "list" is not an inspection flag.
func TestOrdinaryTestRunsAreStillFormatted(t *testing.T) {
	runOutput := "ok  \tgithub.com/x/y\t0.4s\n"
	for _, argv := range [][]string{
		{"go", "test", "./..."},
		{"go", "test", "-run", "TestList", "./..."},
		{"go", "test", "-race", "-count=1", "./..."},
	} {
		in := format.Input{Argv: argv, Command: "go test", Stdout: strings.NewReader(runOutput)}
		if _, err := New().Aggressive(context.Background(), in); errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: an ordinary run must still be formatted", argv)
		}
	}
}
