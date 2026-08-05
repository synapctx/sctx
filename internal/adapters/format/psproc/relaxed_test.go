package psproc

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("drops blank lines and collapses dupes", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader("a\n\nb\nb\nb\n\nc\n")}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		if strings.Contains(body, "\n\n") {
			t.Errorf("body still contains blank line: %q", body)
		}
		if !strings.Contains(body, "b ×3") {
			t.Errorf("body missing dupe marker, got: %q", body)
		}
	})

	t.Run("empty stdout is inapplicable", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader("")}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
