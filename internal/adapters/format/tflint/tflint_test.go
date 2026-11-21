package tflint

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Fixture: tflint v0.59.1 (docker, linux), terraform ruleset preset "all",
// over a one-file module.

func load(t *testing.T, name string, argv ...string) (format.Input, int) {
	t.Helper()
	rd := func(ext string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(rd("exit")))
	o, e := rd("stdout"), rd("stderr")
	return format.Input{Argv: append([]string{"tflint"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	in, rawLen := load(t, "default", "--chdir", "tf")
	got, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "default.golden")
	if *update {
		if err := os.WriteFile(golden, got.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile(golden)
	if string(got.Body) != string(want) {
		t.Fatalf("golden mismatch:\n%s", got.Body)
	}
	if len(got.Body) >= rawLen {
		t.Fatalf("not smaller: %d >= %d", len(got.Body), rawLen)
	}
	raw, _ := os.ReadFile("testdata/default.stdout")
	if n := strings.Count(string(got.Body), "\n  "); n != strings.Count(string(raw), "\nWarning: ")+strings.Count(string(raw), "\nNotice: ")+strings.Count(string(raw), "\nError: ") {
		t.Errorf("kept %d findings:\n%s", n, got.Body)
	}
	for _, w := range []string{"terraform_required_version", "terraform_unused_declarations", "[Fixable]", "tf/outputs.tf", "6 findings"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q:\n%s", w, got.Body)
		}
	}
	if strings.Contains(string(got.Body), "github.com") {
		t.Error("reference link survived")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-f", "json"}, {"--format=sarif"}, {"--format", "checkstyle"}, {"--version"}, {"--init"}} {
		in, _ := load(t, "default", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "{\"issues\":[]}", "TFLint version 0.59.1\n"} {
		in := format.Input{Argv: []string{"tflint"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "default", "--chdir", "tf")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
