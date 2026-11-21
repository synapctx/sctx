package tfsec

import (
	"context"
	"errors"
	"flag"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Fixture: tfsec v1.28.14 (docker, linux) over a one-file module with
// deliberate misconfigurations; coloured output as emitted into a pipe.

func load(t *testing.T, argv ...string) (format.Input, int) {
	t.Helper()
	rd := func(ext string) string {
		b, err := os.ReadFile("testdata/default." + ext)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(rd("exit")))
	o, e := rd("stdout"), rd("stderr")
	return format.Input{Argv: append([]string{"tfsec"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	in, rawLen := load(t, "tf")
	got, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("testdata/default.golden", got.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile("testdata/default.golden")
	if string(got.Body) != string(want) {
		t.Fatalf("golden mismatch:\n%s", got.Body)
	}
	if len(got.Body) >= rawLen {
		t.Fatalf("not smaller: %d >= %d", len(got.Body), rawLen)
	}
	raw, _ := os.ReadFile("testdata/default.stdout")
	nres := strings.Count(string(raw), "Result #")
	if n := strings.Count(string(got.Body), "\n  "); n != nres {
		t.Errorf("kept %d of %d results", n, nres)
	}
	for _, w := range []string{"aws-ec2-no-public-ingress-sgr", "aws-rds-no-public-db-access", "2 passed, 21 potential problem(s) detected.", "…dropped:"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q:\n%s", w, got.Body)
		}
	}
	if strings.Contains(string(got.Body), "\x1b") || strings.Contains(string(got.Body), "xxxxxxxx") {
		t.Error("ANSI or code excerpt survived")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-f", "json", "tf"}, {"--format=sarif", "tf"}, {"--concise-output", "tf"}, {"--version"}} {
		in, _ := load(t, argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "{\"results\":[]}"} {
		in := format.Input{Argv: []string{"tfsec", "tf"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "tf")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
