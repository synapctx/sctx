package rspec

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

// load reads a fixture captured from a real rspec 3.13 run.
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
	tests := []struct {
		name string
		argv []string
		must []string // every one must survive
	}{
		{"pass", []string{"rspec"}, []string{"4 examples, 0 failures, 2 pending", "pending: Calc pending later — not yet (./spec/calc_pass_spec.rb:5)"}},
		{"fail", []string{"rspec"}, []string{
			"1) Calc adds wrong", "expected: 4", "got: 3", "ZeroDivisionError:", "divided by 0",
			"# ./lib/calc.rb:4:in 'Calc#div'", "# ./spec/calc_fail_spec.rb:6", "-:b => [1, 2, 4],", "+:c => \"x\"",
			"undefined method 'upcase' for nil", "5 examples, 4 failures",
			"rerun: rspec ./spec/calc_fail_spec.rb:3 ./spec/calc_fail_spec.rb:4 ./spec/calc_fail_spec.rb:5 ./spec/calc_fail_spec.rb:10",
		}},
		{"doc", []string{"rspec", "--format", "documentation"}, []string{"1) Calc adds wrong", "9 examples, 4 failures, 2 pending", "rerun: rspec "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, code := load(t, tt.name)
			r, err := New().Aggressive(context.Background(), input(tt.argv, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Body) >= len(raw) {
				t.Errorf("render %d bytes is not smaller than raw %d", len(r.Body), len(raw))
			}
			body := string(r.Body)
			for _, m := range tt.must {
				if !strings.Contains(body, m) {
					t.Errorf("lost %q", m)
				}
			}
			golden(t, tt.name, r.Body)
		})
	}
}

func TestDeclines(t *testing.T) {
	loadErr, _ := load(t, "loaderr")
	tests := []struct {
		name string
		argv []string
		out  string
	}{
		{"json format", []string{"rspec", "--format", "json"}, `{"version":"3.13"}`},
		{"json short", []string{"rspec", "-fj"}, `{}`},
		{"json equals", []string{"rspec", "--format=json"}, `{}`},
		{"junit custom", []string{"rspec", "-f", "RspecJunitFormatter"}, `<xml/>`},
		{"not rspec output", []string{"rspec"}, "hello\n"},
		{"summary reports failures we cannot find", []string{"rspec"}, "F\n\n2 examples, 1 failure\n"},
		{"doc format with load error", []string{"rspec", "-f", "d"}, loadErr},
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

func TestLoadErrorKeepsSignal(t *testing.T) {
	raw, code := load(t, "loaderr")
	r, err := New().Aggressive(context.Background(), input([]string{"rspec"}, raw, code))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"An error occurred while loading ./spec/broken_load_spec.rb", "LoadError:", "cannot load such file -- does_not_exist_zzz", "# ./spec/broken_load_spec.rb:1", "1 error occurred outside of examples"} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("lost %q in %q", m, r.Body)
		}
	}
	golden(t, "loaderr", r.Body)
}

func TestGemFramesAreCountedNotSilentlyDropped(t *testing.T) {
	raw := "F\n\nFailures:\n\n  1) X boom\n     Failure/Error: foo\n\n     RuntimeError:\n       bad\n" +
		"     # /app/vendor/bundle/ruby/gems/a-1/lib/a.rb:1:in 'x'\n     # /app/vendor/bundle/ruby/gems/a-1/lib/a.rb:2:in 'y'\n     # ./spec/x_spec.rb:3:in 'block (2 levels) in <top (required)>'\n\n" +
		"Finished in 0.1 seconds\n1 example, 1 failure\n\nFailed examples:\n\nrspec ./spec/x_spec.rb:3 # X boom\n"
	r, err := New().Aggressive(context.Background(), input([]string{"rspec"}, raw, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"# …+2 gem frames", "# ./spec/x_spec.rb:3\n", "rerun: rspec ./spec/x_spec.rb:3"} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("missing %q in %q", m, r.Body)
		}
	}
}

func TestRelaxed(t *testing.T) {
	raw, code := load(t, "fail")
	r, err := New().Relaxed(context.Background(), input([]string{"rspec"}, raw, code))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Body), "\n\n") || strings.HasPrefix(string(r.Body), "FFF") {
		t.Errorf("blank/progress lines survived: %q", r.Body)
	}
	if !strings.Contains(string(r.Body), "expected: 4") {
		t.Error("lost failure message")
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"rspec", "-f", "json"}, "{}", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("json relaxed err = %v", err)
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"rspec"}, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("empty relaxed err = %v", err)
	}
}

func TestStderrIsFolded(t *testing.T) {
	raw, code := load(t, "pass")
	in := input([]string{"rspec"}, raw, code)
	in.Stderr = strings.NewReader("warning: something\n")
	r, err := New().Aggressive(context.Background(), in)
	if err != nil || !r.FoldStderr || !strings.Contains(string(r.Body), "warning: something") {
		t.Fatalf("stderr not folded: %v %q", err, r.Body)
	}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "rspec" {
		t.Fatal("wrong command")
	}
}
