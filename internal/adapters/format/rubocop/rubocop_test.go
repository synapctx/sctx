package rubocop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// load reads a fixture captured from a real rubocop 1.92 run.
func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile(filepath.Join("testdata", name+".exit"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(e)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

func input(argv []string, out string, code int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(out), ExitCode: code}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s mismatch\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

func TestAggressiveGoldens(t *testing.T) {
	for _, name := range []string{"offenses", "few", "simple", "autocorrect"} {
		t.Run(name, func(t *testing.T) {
			raw, code := load(t, name)
			argv := []string{"rubocop", "lib"}
			if name == "simple" {
				argv = append(argv, "--format", "simple")
			}
			r, err := New().Aggressive(context.Background(), input(argv, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Body) >= len(raw) {
				t.Errorf("render %d bytes is not smaller than raw %d", len(r.Body), len(raw))
			}
			golden(t, name, r.Body)
		})
	}
}

// TestEveryOffenseSurvives counts positions in the render against the raw
// headers: grouping may fold messages but must keep every line:col.
func TestEveryOffenseSurvives(t *testing.T) {
	for _, name := range []string{"offenses", "few", "simple", "autocorrect"} {
		t.Run(name, func(t *testing.T) {
			raw, code := load(t, name)
			r, err := New().Aggressive(context.Background(), input([]string{"rubocop"}, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			for _, l := range strings.Split(raw, "\n") {
				if offenseRE.MatchString(l) || simpleRE.MatchString(l) {
					want++
				}
			}
			got := 0
			for _, l := range strings.Split(string(r.Body), "\n") {
				if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "    ") {
					got += strings.Count(strings.Fields(l)[0], ",") + 1
				}
			}
			if got != want || want == 0 {
				t.Errorf("offense positions: got %d want %d", got, want)
			}
		})
	}
}

func TestContentKept(t *testing.T) {
	raw, code := load(t, "offenses")
	r, err := New().Aggressive(context.Background(), input([]string{"rubocop", "lib"}, raw, code))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{
		"lib/calc.rb (9)", "lib/messy2.rb (3)",
		"2:11,2:14,3:11,3:14 C Naming/MethodParameterName: Method parameter must be at least 3 characters long.",
		"3:5 W fixable Lint/UselessAssignment: Useless assignment to variable - x.",
		"cops: Naming/MethodParameterName×7, Layout/EmptyLineBetweenDefs×3", "3 files inspected, 25 offenses detected, 16 offenses autocorrectable",
	} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("lost %q", m)
		}
	}
	if strings.Contains(string(r.Body), "^^^") {
		t.Error("caret echo survived")
	}
}

func TestCleanRun(t *testing.T) {
	raw := "Inspecting 2 files\n..\n\n2 files inspected, no offenses detected\n"
	r, err := New().Aggressive(context.Background(), input([]string{"rubocop"}, raw, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Body) != "2 files inspected, no offenses detected\n" {
		t.Errorf("body = %q", r.Body)
	}
}

func TestDeclines(t *testing.T) {
	raw, _ := load(t, "json")
	tests := []struct {
		name string
		argv []string
		out  string
	}{
		{"json", []string{"rubocop", "--format", "json"}, raw},
		{"json short", []string{"rubocop", "-fj"}, raw},
		{"json equals", []string{"rubocop", "--format=json", "x"}, raw},
		{"github", []string{"rubocop", "-f", "github"}, "::error file=a.rb"},
		{"show cops", []string{"rubocop", "--show-cops"}, "Style/X:\n"},
		{"no summary", []string{"rubocop"}, "hello\n"},
		{"summary miscounts", []string{"rubocop"}, "a.rb:1:1: C: Style/X: msg\nx\n^\n\n1 file inspected, 3 offenses detected\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New().Aggressive(context.Background(), input(tt.argv, tt.out, 1))
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestRelaxedAndStderr(t *testing.T) {
	raw, code := load(t, "offenses")
	in := input([]string{"rubocop"}, raw, code)
	in.Stderr = strings.NewReader("warning: config\n")
	r, err := New().Relaxed(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Body), "\n\n") || strings.HasPrefix(string(r.Body), "CWC") || !r.FoldStderr ||
		!strings.Contains(string(r.Body), "warning: config") || !strings.Contains(string(r.Body), "lib/calc.rb:7:5") {
		t.Errorf("relaxed body wrong: %q", r.Body)
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"rubocop", "-f", "json"}, "{}", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("json relaxed err = %v", err)
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"rubocop"}, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("empty relaxed err = %v", err)
	}
	in = input([]string{"rubocop"}, raw, code)
	in.Stderr = strings.NewReader("warning: config\n")
	agg, err := New().Aggressive(context.Background(), in)
	if err != nil || !strings.Contains(string(agg.Body), "warning: config") {
		t.Errorf("aggressive dropped stderr: %v %q", err, agg.Body)
	}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "rubocop" {
		t.Fatal("wrong command")
	}
}
