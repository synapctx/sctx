package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxedFilter(t *testing.T) {
	f := New()

	t.Run("drops blank and separator lines, dedupes repeats", func(t *testing.T) {
		raw := "header\n\n----\nrepeated\nrepeated\nrepeated\nfooter\n"
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader(raw)}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		if strings.Contains(body, "----") {
			t.Errorf("body still contains separator: %q", body)
		}
		if !strings.Contains(body, "repeated ×3") {
			t.Errorf("body missing dedupe marker, got: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("empty input is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader("")}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
