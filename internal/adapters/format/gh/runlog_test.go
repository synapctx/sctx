package gh

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var updateRunLog = flag.Bool("update-runlog", false, "rewrite run log goldens")

// Fixtures are real `gh run view --log[-failed]` output from public repos.
func TestRunLogGolden(t *testing.T) {
	tests := []struct {
		name, flag string
		must       []string
	}{
		{"run-log-failed", "--log-failed", []string{"== aarch64-pc-windows-gnullvm (windows-11-arm)", "##[error]Error: The process", "failed with exit code 127", "▸ Saving package cache..."}},
		{"run-log-named-steps", "--log", []string{"== docs", "-- Checkout repository", "-- Install Rust"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.name+".stdout"))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New().Aggressive(context.Background(), format.Input{
				Argv: []string{"gh", "run", "view", "-R", "o/r", "1", tc.flag}, Stdout: strings.NewReader(string(raw)),
			})
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tc.name+".golden")
			if *updateRunLog {
				_ = os.WriteFile(golden, out.Body, 0o644)
			}
			want, _ := os.ReadFile(golden)
			if string(out.Body) != string(want) {
				t.Fatalf("golden mismatch:\n%s", out.Body)
			}
			if len(out.Body)*3 > len(raw) {
				t.Errorf("weak saving: %d of %d", len(out.Body), len(raw))
			}
			if !out.Elided {
				t.Error("collapsed groups are content: Elided must be set")
			}
			for _, m := range tc.must {
				if !strings.Contains(string(out.Body), m) {
					t.Errorf("lost %q", m)
				}
			}
			if strings.Contains(string(out.Body), "T08:") || strings.Contains(string(out.Body), "\xef\xbb\xbf") {
				t.Error("timestamp or BOM leaked")
			}
		})
	}
}

func TestRunLogDeclinesUnknownShape(t *testing.T) {
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "run", "view", "1", "--log"}, Stdout: strings.NewReader("no tabs here\nat all\n"),
	})
	if err != format.ErrTierInapplicable {
		t.Fatalf("got %v", err)
	}
}
