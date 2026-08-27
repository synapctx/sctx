package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveWorktree(t *testing.T) {
	f := New()

	t.Run("caps long worktree list", func(t *testing.T) {
		var lines []string
		for i := range 30 {
			lines = append(lines, fmt.Sprintf("/repo/wt-%d  850a312 [branch-%d]", i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "worktree", "list"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "…+10 more worktrees") {
			t.Errorf("body missing elision marker: %q", out.Body)
		}
	})
}
