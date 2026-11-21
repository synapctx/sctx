package semgrep

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

// Fixtures: semgrep 1.140.0 (docker, linux) over a small python project,
// stdout and stderr captured separately.

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
	return format.Input{Argv: append([]string{"semgrep"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	for _, tc := range []string{"local", "auto", "multi", "clean"} {
		t.Run(tc, func(t *testing.T) {
			in, rawLen := load(t, tc, "scan", "--config", "x", "py")
			got, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tc+".golden")
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
			if !got.FoldStderr {
				t.Fatal("FoldStderr must be set")
			}
		})
	}
}

func TestEveryFindingKept(t *testing.T) {
	in, _ := load(t, "multi", "scan", "--error", "py")
	raw, _ := os.ReadFile("testdata/multi.stdout")
	want := strings.Count(string(raw), "❱")
	got, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	have := strings.Count(string(got.Body), "\n  ")
	if have != want {
		t.Errorf("kept %d of %d findings:\n%s", have, want, got.Body)
	}
	for _, w := range []string{"py/pkg/db.py (2)", "10 warning py.no-shell-true", "19 error py.no-eval", "7 findings in 2 files", "…+"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q:\n%s", w, got.Body)
		}
	}
	if strings.Contains(string(got.Body), "subprocess.call(") || strings.Contains(string(got.Body), "sg.run") {
		t.Error("code excerpt or details link survived")
	}
}

func TestConfigErrorNotLost(t *testing.T) {
	in, _ := load(t, "badcfg", "scan", "--config", "nosuch.yml", "py")
	got, err := New().Aggressive(context.Background(), in)
	if err == nil && !strings.Contains(string(got.Body), "invalid configuration file") {
		t.Fatalf("config error lost:\n%s", got.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"scan", "--json"}, {"scan", "--sarif", "py"}, {"scan", "--junit-xml"}, {"scan", "-o", "x.txt"}, {"scan", "--emacs"}} {
		in, _ := load(t, "local", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "{\"results\": []}"} {
		in := format.Input{Argv: []string{"semgrep", "scan"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "local", "scan")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
