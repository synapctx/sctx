package mypy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Captures from the real mypy binary (stdout; the exit code was 1, or 2 when
// the syntax errors prevented further checking).
func TestRealFixtures(t *testing.T) {
	f := New()
	ctx := context.Background()
	in := func(raw string) format.Input {
		return format.Input{Argv: []string{"mypy", "pkg"}, Stdout: strings.NewReader(raw), ExitCode: 1}
	}

	t.Run("errors keep every message code and the summary", func(t *testing.T) {
		raw := load(t, "mypy_fail")
		out, err := f.Aggressive(ctx, in(raw))
		if err != nil {
			t.Fatal(err)
		}
		body := string(out.Body)
		for _, k := range []string{"pkg/core.py — 5 diagnostics",
			`L12 error: Incompatible return value type (got "int", expected "str") [return-value]`,
			`L16 error: Item "None" of "int | None" has no attribute "bit_length" [union-attr]`,
			"[name-defined]", `[arg-type]`, "Found 5 errors in 1 file (checked 3 source files)"} {
			if !strings.Contains(body, k) {
				t.Errorf("missing %q in %q", k, body)
			}
		}
	})

	t.Run("syntax errors", func(t *testing.T) {
		out, err := f.Aggressive(ctx, in(load(t, "mypy_syntax")))
		if err != nil {
			t.Fatal(err)
		}
		body := string(out.Body)
		for _, k := range []string{"L3 error: Expected an expression [syntax]", "errors prevented further checking"} {
			if !strings.Contains(body, k) {
				t.Errorf("missing %q", k)
			}
		}
	})

	t.Run("clean run", func(t *testing.T) {
		out, err := f.Aggressive(ctx, in(load(t, "mypy_clean")))
		if err != nil || !strings.Contains(string(out.Body), "Success: no issues found in 1 source file") {
			t.Fatalf("err=%v body=%q", err, out.Body)
		}
	})

	t.Run("--pretty wrapping is never truncated", func(t *testing.T) {
		raw := load(t, "mypy_pretty")
		if _, err := f.Aggressive(ctx, in(raw)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("aggressive err = %v, want decline (wrapped messages are not modelled)", err)
		}
		out, err := f.Relaxed(ctx, in(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"str")  [return-value]`, `"bit_length"  [union-attr]`, `^~~~~~~~~~~~`} {
			if !strings.Contains(string(out.Body), k) {
				t.Errorf("relaxed lost %q", k)
			}
		}
	})
}
