package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveReflog(t *testing.T) {
	f := New()

	t.Run("caps long reflog", func(t *testing.T) {
		var lines []string
		for i := range 50 {
			lines = append(lines, fmt.Sprintf("850a312 HEAD@{%d}: commit: change %d", i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"

		in := format.Input{
			Argv:   []string{"git", "reflog"},
			Stdout: strings.NewReader(raw),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "HEAD@{0}") {
			t.Errorf("body missing most recent entry: %q", body)
		}
		if !strings.Contains(body, "…+20 more") {
			t.Errorf("body missing elision marker: %q", body)
		}
	})

	t.Run("short reflog is inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "reflog"},
			Stdout: strings.NewReader("850a312 HEAD@{0}: commit: change\n"),
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
