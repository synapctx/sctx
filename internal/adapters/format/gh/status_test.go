package gh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveStatusCapsEachSection(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("Relevant pull requests in cli/cli\n\nCurrent branch\n")
	for i := 1; i <= 13; i++ {
		fmt.Fprintf(&raw, "  #%d  pull request title %d [branch-%d]\n", i, i, i)
	}
	raw.WriteString("\nCreated by you\n")
	for i := 20; i <= 31; i++ {
		fmt.Fprintf(&raw, "  #%d  another pull request title [branch]\n", i)
	}
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "pr", "status"}, Stdout: strings.NewReader(raw.String()),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	got := string(out.Body)
	if !strings.Contains(got, "…+3 more entries") || !strings.Contains(got, "…+2 more entries") {
		t.Fatalf("missing per-section markers: %q", got)
	}
}
