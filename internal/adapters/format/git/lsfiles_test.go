package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveLsFiles(t *testing.T) {
	f := New()

	t.Run("groups by directory and caps names per dir", func(t *testing.T) {
		var lines []string
		for i := range 20 {
			lines = append(lines, fmt.Sprintf("internal/pkg/file%02d.go", i))
		}
		lines = append(lines, "main.go", "go.mod")
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "ls-files"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "internal/pkg/") {
			t.Errorf("body missing directory grouping: %q", body)
		}
		if !strings.Contains(body, "…+10") {
			t.Errorf("body missing per-dir elision marker: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})
}
