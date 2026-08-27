package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveStash(t *testing.T) {
	f := New()

	t.Run("caps long stash list", func(t *testing.T) {
		var lines []string
		for i := range 30 {
			lines = append(lines, fmt.Sprintf("stash@{%d}: WIP on main: 850a312 change %d", i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "stash", "list"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "…+10 more stashes") {
			t.Errorf("body missing elision marker: %q", out.Body)
		}
	})

	t.Run("non-list stash subcommand is inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "stash", "push"},
			Stdout: strings.NewReader("Saved working directory and index state WIP on main: 850a312 x\n"),
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
