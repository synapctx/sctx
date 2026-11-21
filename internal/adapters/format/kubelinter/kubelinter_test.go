package kubelinter

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

// Fixtures: kube-linter 0.8.3 (docker, linux) linting a deployment with a
// privileged container, a latest tag and no resource requests.

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
	return format.Input{Argv: append([]string{"kube-linter"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	in, rawLen := load(t, "default", "lint", "k8s")
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
	for _, w := range []string{"privileged-container", "latest-tag", "run-as-non-root", "Deployment/web", "Error: found 7 lint errors", "…dropped: 7 remediation hints"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q:\n%s", w, got.Body)
		}
	}
	if n := strings.Count(string(got.Body), "\n  - "); n != 7 {
		t.Errorf("kept %d of 7 findings", n)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"lint", "--format", "json", "k8s"}, {"lint", "--format=sarif", "k8s"}, {"checks", "list"}, {"version"}} {
		in, _ := load(t, "default", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	in, _ := load(t, "ok", "lint", "k8s/ok.yaml") // clean run: already tiny
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("clean: %v", err)
	}
	for _, s := range []string{"", "random text\nmore\n", "[{\"x\":1}]"} {
		in := format.Input{Argv: []string{"kube-linter", "lint", "x"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ = load(t, "default", "lint", "k8s")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
