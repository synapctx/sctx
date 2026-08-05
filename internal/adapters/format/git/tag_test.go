package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveTag(t *testing.T) {
	f := New()

	t.Run("caps long tag list", func(t *testing.T) {
		var lines []string
		for i := 0; i < 60; i++ {
			lines = append(lines, fmt.Sprintf("v1.%d.0", i))
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "tag"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "…+20 more tags") {
			t.Errorf("body missing elision marker: %q", out.Body)
		}
	})

	t.Run("short tag list is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"git", "tag"}, Stdout: strings.NewReader("v1.0.0\nv1.1.0\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
