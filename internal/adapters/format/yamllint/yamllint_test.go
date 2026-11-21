package yamllint

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

func input(t *testing.T, name string, argv ...string) (format.Input, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".stdout"))
	if err != nil {
		t.Fatal(err)
	}
	return format.Input{Argv: append([]string{"yamllint"}, argv...), Stdout: strings.NewReader(string(b)), ExitCode: 1}, string(b)
}

func TestGolden(t *testing.T) {
	in, raw := input(t, "default", "x")
	got, err := New().Aggressive(context.Background(), in)
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
	if len(got.Body) >= len(raw) {
		t.Fatalf("not smaller: %d >= %d", len(got.Body), len(raw))
	}
	if strings.Contains(string(got.Body), "\x1b") {
		t.Fatal("ANSI leaked")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-f", "json", "x"}, {"--format=parsable", "x"}, {"--format", "checkstyle", "x"}} {
		in, _ := input(t, "default", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n"} {
		in := format.Input{Argv: []string{"yamllint", "x"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in := format.Input{Argv: []string{"yamllint", "x"}, Stdout: strings.NewReader(""), Stderr: strings.NewReader("boom")}
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("stderr: %v", err)
	}
}
