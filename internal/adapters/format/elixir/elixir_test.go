package elixir

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

// Fixtures were captured from elixir:1.17 (Elixir 1.17.3) with mix.
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

func TestRendersKeepErrorSignal(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		keep []string
		drop []string
	}{
		{"ex-compile-warn", []string{"mix", "compile"},
			[]string{"warning: variable \"y\" is unused", "└─ lib/demo.ex:4:5: Demo.unused_var/1", "==> demo"},
			[]string{"Generated jason app", "Compiling 10 files", "│     y = 1"}},
		{"ex-compile-err", []string{"mix", "compile"},
			[]string{"== Compilation error in file lib/demo.ex ==", "MismatchedDelimiterError", "error: unexpected reserved word: end", "└─ lib/demo.ex:10:1", "(elixir 1.17.3)"},
			[]string{"Generated jason app"}},
		{"ex-compile-undef", []string{"mix", "compile"},
			[]string{"error: undefined function undefined_fn/1", "└─ lib/demo.ex:4:5: Demo.broken/0", "(CompileError) lib/demo.ex: cannot compile module Demo"},
			[]string{"undefined_fn(1)"}},
		{"ex-test-fail", []string{"mix", "test"},
			[]string{"1) test match (DemoTest)", "test/demo_test.exs:20", "left:  {:ok, _}", "right: {:error, 1}", "Expected exception ArgumentError but got ArithmeticError",
				"4) test list compare (DemoTest)", "test/demo_test.exs:13: (test)", "5 tests, 4 failures", "Running ExUnit with seed: 75191", "variable \"y\" is unused"},
			[]string{"Compiling 2 files"}},
		{"ex-fmt-check", []string{"mix", "format", "--check-formatted"},
			[]string{"mix format failed due to --check-formatted", "/work/ex/lib/ugly.ex", "/work/ex/lib/demo.ex"},
			[]string{"|def f( a,b ) do a+b end"}},
		{"ex-deps", []string{"mix", "deps.get"},
			[]string{"jason 1.4.5", "* Getting jason (Hex package)"}, []string{"Resolution completed"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
			out, err := New().Aggressive(context.Background(), in)
			if tc.name == "ex-deps" {
				// Tiny output: declining is the correct answer when nothing is smaller.
				if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("render %d >= raw %d", len(body), len(raw))
			}
			if !strings.Contains(body, "…+") {
				t.Error("elision not marked")
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
			golden(t, tc.name, out.Body)

			rel, err := New().Relaxed(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code})
			if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatal(err)
			}
			if err == nil {
				for _, k := range tc.keep {
					if !strings.Contains(string(rel.Body), k) && !strings.Contains(k, "Generated") {
						t.Errorf("relaxed lost %q", k)
					}
				}
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	raw, code := load(t, "ex-test-fail")
	for _, argv := range [][]string{
		{"mix", "test", "--trace"}, {"mix", "run", "x.exs"}, {"mix", "phx.server"}, {"mix"}, {"mix", "format", "--stdin"},
		{"mix", "test", "--formatter", "JUnitFormatter"},
	} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}

func TestSuccessfulTestRunStaysSmall(t *testing.T) {
	raw, code := load(t, "ex-test-ok")
	in := format.Input{Argv: []string{"mix", "test"}, Stdout: strings.NewReader(raw), ExitCode: code}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "2 tests, 0 failures") {
		t.Errorf("summary lost: %s", out.Body)
	}
}
