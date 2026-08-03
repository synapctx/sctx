package docker

import (
	"context"
	"fmt"
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

	t.Run("long logs keep head and tail with a marker", func(t *testing.T) {
		var lines []string
		for i := 0; i < 60; i++ {
			lines = append(lines, fmt.Sprintf("2024-01-01T00:00:%02dZ line %d", i%60, i))
		}
		raw := strings.Join(lines, "\n")
		in := format.Input{Argv: []string{"docker", "logs", "web"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "…+35 lines") {
			t.Errorf("body missing elision marker, got: %q", body)
		}
		if !strings.Contains(body, "line 0") || !strings.Contains(body, "line 59") {
			t.Errorf("body missing head/tail content: %q", body)
		}
		if strings.Contains(body, "line 30") {
			t.Errorf("body still contains an elided middle line: %q", body)
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
