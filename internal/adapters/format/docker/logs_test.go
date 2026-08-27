package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveLogs(t *testing.T) {
	f := New()

	t.Run("short logs pass through untouched", func(t *testing.T) {
		raw := "line one\nline two\nline three"
		in := format.Input{Argv: []string{"docker", "logs", "web"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if got := string(out.Body); got != raw {
			t.Errorf("body = %q, want %q", got, raw)
		}
	})

	t.Run("exact repeats collapse but a unique middle error remains", func(t *testing.T) {
		var lines []string
		for range 30 {
			lines = append(lines, "steady-state")
		}
		lines = append(lines, "UNIQUE_ERROR_SENTINEL")
		for range 30 {
			lines = append(lines, "steady-state")
		}
		raw := strings.Join(lines, "\n")
		in := format.Input{Argv: []string{"docker", "logs", "web"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if strings.Count(body, "steady-state ×30") != 2 {
			t.Errorf("body missing exact repeat markers, got: %q", body)
		}
		if !strings.Contains(body, "UNIQUE_ERROR_SENTINEL") {
			t.Errorf("body dropped unique middle error: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("empty is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "logs", "web"}, Stdout: strings.NewReader("")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
