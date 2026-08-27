package gotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const fixApplyStderr = "# github.com/example/mod/lib/a\n" +
	"# [github.com/example/mod/lib/a]\n" +
	"fix: applied 7 of 8 fixes; 3 files updated. (Re-run the command to apply more.)\n" +
	"# github.com/example/mod/lib/b\n" +
	"fix: applied 2 of 2 fixes; 1 file updated.\n"

func TestAggressive_Fix(t *testing.T) {
	f := New()

	t.Run("totals the per-package summaries", func(t *testing.T) {
		in := newInput([]string{"go", "fix", "./..."}, "go fix", "", fixApplyStderr, 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "applied 9 of 10 fixes across 2 packages; 4 files updated") {
			t.Errorf("Body = %q, want the summed totals", body)
		}
		// The banners are the repetition worth dropping.
		if strings.Contains(body, "# github.com/example") {
			t.Errorf("Body = %q, should drop the package banners", body)
		}
	})

	// "applied 7 of 8" means fixes remain. Reporting only a total would let a
	// migration look finished while a package still had pending edits.
	t.Run("surfaces packages that need another pass", func(t *testing.T) {
		in := newInput([]string{"go", "fix", "./..."}, "go fix", "", fixApplyStderr, 0, 0)

		rendered, _ := f.Aggressive(context.Background(), in)
		body := string(rendered.Body)
		if !strings.Contains(body, "1 package(s) have fixes left") {
			t.Errorf("Body = %q, want the incomplete-pass warning", body)
		}
	})

	t.Run("declines when nothing recognizable was printed", func(t *testing.T) {
		in := newInput([]string{"go", "fix", "./..."}, "go fix", "some unexpected output\n", "", 0, 0)

		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("Aggressive() error = %v, want ErrTierInapplicable", err)
		}
	})
}

// A unified diff is the answer, and both tiers would damage it: relaxedFilter
// drops the single-space context line for a blank source line and collapses
// consecutive identical context lines. Both tiers must therefore decline so the
// patch reaches the caller byte-exact.
func TestFix_DiffModeDeclinesEveryTier(t *testing.T) {
	f := New()
	// Contains exactly the two shapes relaxedFilter would damage.
	diff := "--- a.go (old)\n" +
		"+++ a.go (new)\n" +
		"@@ -1,5 +1,5 @@\n" +
		" package p\n" +
		" \n" + // blank context line: a single space
		"-\tfor i := 0; i < 10; i++ {\n" +
		"+\tfor i := range 10 {\n" +
		" \t}\n" +
		" \t}\n" // consecutive identical context lines

	for _, argv := range [][]string{
		{"go", "fix", "-diff", "./..."},
		{"go", "fix", "--diff", "./..."},
	} {
		in := newInput(argv, "go fix", diff, "", 1, 0)
		if _, err := f.Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("Aggressive(%v) error = %v, want ErrTierInapplicable", argv, err)
		}
		in = newInput(argv, "go fix", diff, "", 1, 0)
		if _, err := f.Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("Relaxed(%v) error = %v, want ErrTierInapplicable", argv, err)
		}
	}
}

// Guards the claim in diffMode's comment: this is why the patch may not be
// filtered. If relaxedFilter ever stops damaging a diff, the decline above can
// be revisited -- until then this documents the reason.
func TestRelaxedFilterWouldDamageADiff(t *testing.T) {
	got := relaxedFilter(" package p\n \n \t}\n \t}\n")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "\n \n") {
		t.Error("relaxedFilter now preserves blank context lines; revisit diffMode")
	}
	if !strings.Contains(joined, "×2") {
		t.Error("relaxedFilter no longer collapses repeated lines; revisit diffMode")
	}
}
