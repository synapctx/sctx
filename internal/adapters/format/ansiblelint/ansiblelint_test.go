package ansiblelint

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

// Fixture: ansible-lint 25.9.2 (python:3.12-slim, linux) over a playbook with
// 16 violations; stdout holds the violations, stderr the summary.

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
	return format.Input{Argv: append([]string{"ansible-lint"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	in, rawLen := load(t, "ans/play.yml")
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
	for _, w := range []string{"16 findings in 1 file", "fqcn[action-core]=4", "package-latest", "Failed: 16 failure(s), 0 warning(s)", "Task/Handler: apt name=git state=latest"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q:\n%s", w, got.Body)
		}
	}
	if strings.Contains(string(got.Body), "profile:basic") || strings.Contains(string(got.Body), "Read documentation") {
		t.Error("summary table survived")
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-f", "json", "x"}, {"--format=sarif", "x"}, {"-f", "codeclimate", "x"}, {"-p", "x"}, {"--version"}, {"-L"}} {
		in, _ := load(t, argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "[{\"check_name\":\"x\"}]"} {
		in := format.Input{Argv: []string{"ansible-lint", "x"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "x")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
