package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveBranch(t *testing.T) {
	f := New()

	t.Run("caps long list, always keeps current branch", func(t *testing.T) {
		var lines []string
		lines = append(lines, "* main")
		for i := range 40 {
			lines = append(lines, fmt.Sprintf("  feature-%02d", i))
		}
		raw := strings.Join(lines, "\n") + "\n"

		in := format.Input{
			Argv:   []string{"git", "branch", "-a"},
			Stdout: strings.NewReader(raw),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "* main") {
			t.Errorf("body missing current branch marker: %q", body)
		}
		if !strings.Contains(body, "…+") {
			t.Errorf("body missing elision marker: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("short list is inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "branch"},
			Stdout: strings.NewReader("* main\n  feature-x\n"),
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
