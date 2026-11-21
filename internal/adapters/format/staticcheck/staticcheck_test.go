package staticcheck

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: staticcheck 2026.1 on macOS arm64 over a module with deprecated
// API use, unused code, error-string style findings and a type error.

func inputOf(t *testing.T, name string, argv ...string) (format.Input, string) {
	t.Helper()
	read := func(k string) string {
		b, err := os.ReadFile("testdata/" + name + "." + k + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if len(argv) == 0 {
		argv = []string{"staticcheck", "./..."}
	}
	o, e := read("stdout"), read("stderr")
	return format.Input{Argv: argv, ExitCode: 1, Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, o + e
}

func TestGroupingKeepsEveryFindingAndShrinks(t *testing.T) {
	for _, name := range []string{"sc", "sc2"} {
		t.Run(name, func(t *testing.T) {
			in, raw := inputOf(t, name)
			r, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			var file string
			rebuilt := map[string]int{}
			for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
				if !strings.HasPrefix(l, "  ") {
					file = strings.TrimSuffix(l, ":")
					continue
				}
				pos, msg, _ := strings.Cut(strings.TrimPrefix(l, "  "), " ")
				rebuilt[file+":"+pos+": "+msg]++
			}
			for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
				if rebuilt[l] == 0 {
					t.Errorf("finding lost: %q", l)
				}
				rebuilt[l]--
			}
			if name == "sc2" && len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
			}
			for _, w := range []string{"(SA4017)", "(S1025)", "(U1000)", "(SA1019)", "should use strings.Contains"} {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q", w)
				}
			}
		})
	}
}

func TestCompileErrorsAreKept(t *testing.T) {
	in, _ := inputOf(t, "sc-compile")
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, w := range []string{"-: # example.com/sc", "./x.go:", `2:23 cannot use "s" (untyped string constant) as int value in return statement (compile)`} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in:\n%s", w, body)
		}
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"staticcheck", "-f", "json", "./..."}, {"staticcheck", "-f=sarif", "./..."}, {"staticcheck", "-explain", "SA1019"},
		{"staticcheck", "-list-checks"}, {"staticcheck", "-version"}, {"staticcheck", "-debug.version"}, {"staticcheck", "--f", "stylish"},
	} {
		in, _ := inputOf(t, "sc2", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: err=%v", argv, err)
		}
	}
	in, _ := inputOf(t, "sc2", "staticcheck", "-f", "text", "./...")
	if _, err := New().Aggressive(context.Background(), in); err != nil {
		t.Errorf("-f text must be handled: %v", err)
	}
	empty := format.Input{Argv: []string{"staticcheck"}, Stdout: strings.NewReader(""), Stderr: strings.NewReader("")}
	if _, err := New().Aggressive(context.Background(), empty); !errors.Is(err, format.ErrTierInapplicable) {
		t.Error("clean run must decline")
	}
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Error("relaxed must decline")
	}
}
