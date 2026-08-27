package gh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressivePRDiffNameOnlyCapsFiles(t *testing.T) {
	var raw strings.Builder
	for i := range diffNameCap + 7 {
		fmt.Fprintf(&raw, "pkg/feature/file-%03d.go\n", i)
	}
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "pr", "diff", "1", "--name-only"}, Stdout: strings.NewReader(raw.String()),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if got := string(out.Body); !strings.Contains(got, "…+7 more files") {
		t.Fatalf("missing file marker: %q", got)
	}
}

func TestAggressivePRDiffDelegatesNativePatch(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\nindex 111..222 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,4 +1,4 @@\n-old\n+new\n context\n context\n context\n"
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "pr", "diff", "1"}, Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if got := string(out.Body); !strings.Contains(got, "-old") || !strings.Contains(got, "+new") {
		t.Fatalf("diff lost changed lines: %q", got)
	}
}

func TestAggressivePRDiffColorAlwaysDeclines(t *testing.T) {
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "pr", "diff", "1", "--color=always"}, Stdout: strings.NewReader("\x1b[31m-old\x1b[0m\n"),
	})
	if err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
	}
}
