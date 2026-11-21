package checkov

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Fixtures: checkov 3.2.470 (docker, linux) over terraform and kubernetes
// manifests with deliberate misconfigurations.

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
	return format.Input{Argv: append([]string{"checkov"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	for _, name := range []string{"tf", "k8s", "ok", "quiet"} {
		t.Run(name, func(t *testing.T) {
			in, rawLen := load(t, name, "-d", "x")
			got, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name+".golden")
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
		})
	}
}

// Every FAILED check in the raw report must appear (id + resource) in the render.
func TestEveryFailureKept(t *testing.T) {
	failRE := regexp.MustCompile(`(?m)^Check: (\S+): ".*"\n\tFAILED for resource: (\S+)\n\tFile: (\S+)`)
	for _, name := range []string{"tf", "k8s", "quiet"} {
		in, _ := load(t, name, "-d", "x")
		raw, _ := os.ReadFile("testdata/" + name + ".stdout")
		got, err := New().Aggressive(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		ms := failRE.FindAllStringSubmatch(string(raw), -1)
		if len(ms) == 0 {
			t.Fatalf("%s: fixture has no failures", name)
		}
		for _, m := range ms {
			if !strings.Contains(string(got.Body), "\n  "+m[2]+" "+m[3]) {
				t.Errorf("%s: lost failure %s on %s %s", name, m[1], m[2], m[3])
			}
			if !strings.Contains(string(got.Body), m[1]+" ") {
				t.Errorf("%s: lost check id %s", name, m[1])
			}
		}
		if strings.Contains(string(got.Body), "Guide:") || strings.Contains(string(got.Body), " | ") {
			t.Errorf("%s: guide/code survived", name)
		}
		if !strings.Contains(string(got.Body), "Passed checks:") || !strings.Contains(string(got.Body), "…+") {
			t.Errorf("%s: totals or marker missing", name)
		}
	}
}

func TestMissingDirErrorKept(t *testing.T) {
	in, _ := load(t, "missing", "-d", "nosuchdir")
	got, err := New().Aggressive(context.Background(), in)
	if err == nil && !strings.Contains(string(got.Body), "does not exist") {
		t.Fatalf("error lost:\n%s", got.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"-d", "x", "-o", "json"}, {"-d", "x", "--output=sarif"}, {"-d", "x", "--output", "junitxml"}, {"--version"}, {"-l"}} {
		in, _ := load(t, "tf", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	in, _ := load(t, "tf", "-d", "x", "-o", "cli")
	if _, err := New().Aggressive(context.Background(), in); err != nil {
		t.Errorf("-o cli should render: %v", err)
	}
	for _, s := range []string{"", "random text\nmore\n", "{\"results\":{}}"} {
		in := format.Input{Argv: []string{"checkov", "-d", "x"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ = load(t, "tf", "-d", "x")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
