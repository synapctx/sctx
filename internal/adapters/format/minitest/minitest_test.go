package minitest

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

// load reads a fixture captured from a real minitest 6.1 / rake 13 run.
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
		must []string
	}{
		{"rake-pass", []string{"rake", "test"}, []string{"3 runs, 2 assertions, 0 failures, 0 errors, 1 skips"}},
		{"ruby-pass", []string{"ruby", "-Itest", "test/ok_test.rb"}, []string{"3 runs, 2 assertions"}},
		{"rake-fail", []string{"rake", "test"}, []string{
			"1) Error:", "BadTest#test_div_zero:", "ZeroDivisionError: divided by 0", "lib/calc.rb:4:in 'Integer#/'",
			"test/bad_test.rb:5:in 'BadTest#test_div_zero'", "BadTest#test_text [test/bad_test.rb:11]:", "-line2", "+lineX",
			"BadTest#test_nil [test/bad_test.rb:9]:", "Expected \"x\" to be nil.", "BadTest#test_multi [test/bad_test.rb:7]:",
			"Expected: {a: 1, b: [1, 2, 3]}", "  Actual: {a: 1, b: [1, 2, 4]}", "BadTest#test_add_wrong [test/bad_test.rb:4]:",
			"6 runs, 5 assertions, 4 failures, 1 errors, 0 skips (seed 56532)", "…+6 rake abort lines",
		}},
		{"ruby-fail", []string{"ruby", "-Itest", "test/bad_test.rb"}, []string{"BadTest#test_add_wrong [test/bad_test.rb:4]:", "4 failures, 1 errors"}},
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
			for _, m := range tt.must {
				if !strings.Contains(string(r.Body), m) {
					t.Errorf("lost %q", m)
				}
			}
			golden(t, tt.name, r.Body)
		})
	}
}

func TestGemFramesCounted(t *testing.T) {
	raw := "Run options: --seed 1\n\n# Running:\n\nE\n\nFinished in 0.1s\n\n  1) Error:\nXTest#test_a:\nRuntimeError: boom\n" +
		"    /app/vendor/bundle/ruby/4.0.0/gems/x-1/lib/x.rb:1:in 'a'\n    /app/vendor/bundle/ruby/4.0.0/gems/x-1/lib/x.rb:2:in 'b'\n    test/x_test.rb:3:in 'XTest#test_a'\n\n1 runs, 0 assertions, 0 failures, 1 errors, 0 skips\n"
	r, err := NewRails().Aggressive(context.Background(), input([]string{"rails", "test"}, raw, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"…+2 gem frames", "test/x_test.rb:3:in 'XTest#test_a'", "RuntimeError: boom", "(seed 1)"} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("missing %q in %q", m, r.Body)
		}
	}
}

func TestDeclines(t *testing.T) {
	tests := []struct{ name, out string }{
		{"not minitest", "hello world\n"},
		{"empty", ""},
		{"summary reports failures we cannot find", "1 runs, 1 assertions, 1 failures, 0 errors, 0 skips\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewRuby().Aggressive(context.Background(), input([]string{"ruby", "x.rb"}, tt.out, 1)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestRelaxedOnlyForMinitestShape(t *testing.T) {
	if _, err := NewRuby().Relaxed(context.Background(), input([]string{"ruby", "x.rb"}, "line\n\nline2\n", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("non-minitest relaxed err = %v", err)
	}
	raw, code := load(t, "rake-fail")
	r, err := New().Relaxed(context.Background(), input([]string{"rake", "test"}, raw, code))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Body), "\n\n") || !strings.Contains(string(r.Body), "Expected: 4") {
		t.Errorf("relaxed body wrong: %q", r.Body)
	}
}

func TestStderrFolded(t *testing.T) {
	raw, code := load(t, "rake-pass")
	in := input([]string{"rake"}, raw, code)
	in.Stderr = strings.NewReader("warning: x\n")
	r, err := New().Aggressive(context.Background(), in)
	if err != nil || !r.FoldStderr || !strings.Contains(string(r.Body), "warning: x") {
		t.Fatalf("stderr not folded: %v %q", err, r.Body)
	}
}

func TestDescriptors(t *testing.T) {
	if m := New().Descriptor(); m.Command != "rake" {
		t.Errorf("rake descriptor %+v", m)
	}
	if m := NewRails().Descriptor(); m.Command != "rails" || len(m.Subcommands) != 1 || m.Subcommands[0] != "test" {
		t.Errorf("rails descriptor %+v", m)
	}
	if m := NewRuby().Descriptor(); m.Command != "ruby" {
		t.Errorf("ruby descriptor %+v", m)
	}
}
