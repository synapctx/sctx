package actionlint

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

func input(t *testing.T, argv ...string) format.Input {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "default.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	return format.Input{Argv: append([]string{"actionlint"}, argv...), Stdout: strings.NewReader(string(b)), ExitCode: 1}
}

func TestGolden(t *testing.T) {
	got, err := New().Aggressive(context.Background(), input(t))
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "default.golden")
	if *update {
		_ = os.WriteFile(golden, got.Body, 0o644)
	}
	want, _ := os.ReadFile(golden)
	if string(got.Body) != string(want) {
		t.Fatalf("golden mismatch:\n%s", got.Body)
	}
	raw, _ := os.ReadFile(filepath.Join("testdata", "default.stdout"))
	if len(got.Body) >= len(raw) {
		t.Fatal("not smaller")
	}
	// every finding keeps its position and kind
	for _, w := range []string{"bad.yml (6)", "5:14", "8:23", "10:29", "11:9", "13:3", "[runner-label]", "[expression]", "[shellcheck]", "[job-needs]", "…+"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-oneline"}, {"-format", "{{json .}}"}, {"-version"}} {
		if _, err := New().Aggressive(context.Background(), input(t, argv...)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	junk := format.Input{Argv: []string{"actionlint"}, Stdout: strings.NewReader("something odd\n")}
	if _, err := New().Aggressive(context.Background(), junk); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("junk: %v", err)
	}
}
