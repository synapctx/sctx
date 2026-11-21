package markdownlint

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGolden(t *testing.T) {
	raw := read(t, "default.stderr")
	got, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"markdownlint", "bad.md", "big.md"}, Stdout: strings.NewReader(""), Stderr: strings.NewReader(raw), ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "default.golden")
	if *update {
		_ = os.WriteFile(golden, got.Body, 0o644)
	}
	if want := read(t, "default.golden"); string(got.Body) != want {
		t.Fatalf("golden mismatch:\n%s", got.Body)
	}
	if !got.FoldStderr {
		t.Error("findings come from stderr; FoldStderr must be set")
	}
	if len(got.Body) >= len(raw) {
		t.Fatal("not smaller")
	}
	// every raw finding line survives as a rendered row or a counted repeat
	n := strings.Count(raw, " error MD")
	if !strings.Contains(string(got.Body), "markdownlint: 32 findings") || n != 32 {
		t.Fatalf("finding count off (raw %d):\n%s", n, got.Body)
	}
}

func TestDeclines(t *testing.T) {
	raw := read(t, "default.stderr")
	tests := []struct {
		name   string
		argv   []string
		stdout string
		stderr string
	}{
		{"json", []string{"markdownlint", "--json", "x.md"}, "", raw},
		{"output file", []string{"markdownlint", "-o", "out.txt", "x.md"}, "", raw},
		{"fix", []string{"markdownlint", "--fix", "x.md"}, "", raw},
		{"stdout present", []string{"markdownlint", "x.md"}, "[]", raw},
		{"no match", []string{"markdownlint", "x.md"}, "", read(t, "nomatch.stderr")},
		{"empty", []string{"markdownlint", "x.md"}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New().Aggressive(context.Background(), format.Input{
				Argv: tc.argv, Stdout: strings.NewReader(tc.stdout), Stderr: strings.NewReader(tc.stderr),
			})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want decline, got %v", err)
			}
		})
	}
}
