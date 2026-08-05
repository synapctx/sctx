package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveShortlog(t *testing.T) {
	f := New()

	t.Run("caps long contributor list", func(t *testing.T) {
		var lines []string
		for i := 30; i > 0; i-- {
			lines = append(lines, fmt.Sprintf("  %3d  Contributor %d", i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "shortlog", "-sn"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "…+10 more contributors") {
			t.Errorf("body missing elision marker: %q", out.Body)
		}
	})
}
