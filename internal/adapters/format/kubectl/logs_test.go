package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveLogs(t *testing.T) {
	f := New()

	t.Run("unique logs decline to verbatim", func(t *testing.T) {
		raw := "line one\nline two\nline three"
		in := format.Input{Argv: []string{"kubectl", "logs", "web"}, Stdout: strings.NewReader(raw)}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Fatalf("Aggressive() error = %v, want inapplicable", err)
		}
	})

	t.Run("repeated logs collapse but preserve unique middle error", func(t *testing.T) {
		raw := strings.Repeat("steady-line\n", 40) + "UNIQUE_ERROR_SENTINEL\n" + strings.Repeat("steady-line\n", 10)
		in := format.Input{Argv: []string{"kubectl", "logs", "web"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "steady-line ×40") || !strings.Contains(body, "steady-line ×10") {
			t.Errorf("body missing exact run markers, got: %q", body)
		}
		if !strings.Contains(body, "UNIQUE_ERROR_SENTINEL") {
			t.Errorf("body lost unique middle diagnostic: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("empty is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "logs", "web"}, Stdout: strings.NewReader("")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
