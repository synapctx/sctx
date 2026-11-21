package erlang

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

// Fixtures were captured from elixir:1.17 (erlang:27 (rebar3 3.24).
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
		{"er-compile-warn", []string{"rebar3", "compile"},
			[]string{"src/demo.erl:6: Warning: function unused_fn/0 is unused", "src/demo.erl:7: Warning: function shadow/1 is unused", "src/demo.erl:7: Warning: variable 'Y' is unused"},
			[]string{"===> Verifying", "┌─", "unused_fn() -> ok."}},
		{"er-compile-err", []string{"rebar3", "compile"},
			[]string{"===> Compiling src/demo.erl failed", "src/demo.erl:5: syntax error before:", "src/demo.erl:4: Warning: variable 'B' is unused"},
			[]string{"oops(X) -> undefined_mod:nope(X", "===> Analyzing"}},
		{"er-eunit-fail", []string{"rebar3", "eunit"},
			[]string{"Failures:", "1) demo_tests:add_wrong_test/0", "expected: 4", "got: 3", "%% /work/proj/test/demo_tests.erl:5:in `demo_tests:add_wrong_test/0`", "expected: \"hi bob\"",
				"3) demo_tests:boom_test/0", "{error,kaboom,", "{demo_tests,boom_test,0,", "4 tests, 3 failures", "===> Error running tests", "test/demo_SUITE.erl:2: Warning: export_all flag enabled"},
			[]string{"eunit_proc", "eunit_test.erl", "Finished in", "===> Performing EUnit"}},
		{"er-eunit-ok", []string{"rebar3", "eunit"}, []string{"2 tests, 0 failures"}, []string{"Finished in", "Verifying"}},
		{"er-ct-fail", []string{"rebar3", "ct"},
			[]string{"%%% demo_SUITE ==> bad_case: FAILED", "{{badmatch,3},", "{demo_SUITE,bad_case,1,", "Failed 1 tests. Passed 1 tests.", "===> Failures occurred running tests: 1"},
			[]string{"test_server", "EXPERIMENTAL", "Results written", "%%% demo_SUITE: ."}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
			out, err := New().Aggressive(context.Background(), in)
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
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(rel.Body), "…+") {
				t.Error("relaxed elision not marked")
			}
		})
	}
}

func TestEscapedGlyphsAreParsedToo(t *testing.T) {
	raw := "===> Compiling demo\n   \\x{250C}\\x{2500} src/demo.erl:\n   \\x{2502}\n 6 \\x{2502}  unused_fn() -> ok.\n   \\x{2502}  \\x{2570}\\x{2500}\\x{2500} Warning: function unused_fn/0 is unused\n\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"rebar3", "compile"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "src/demo.erl:6: Warning: function unused_fn/0 is unused") {
		t.Errorf("got %q", out.Body)
	}
}

func TestMalformedBoxPassesThrough(t *testing.T) {
	raw := "===> Verifying dependencies...\n===> Analyzing applications...\n===> Compiling demo\n   ┌─ src/demo.erl:\n   │\n 6 │  x\n   │  ╰── first\n   │      second line\n\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"rebar3", "compile"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "second line") {
		t.Errorf("message tail lost: %q", out.Body)
	}
}

func TestDeclines(t *testing.T) {
	raw, code := load(t, "er-eunit-fail")
	for _, argv := range [][]string{{"rebar3"}, {"rebar3", "shell"}, {"rebar3", "release"}, {"rebar3", "tree"}, {"rebar3", "dialyzer"}} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
	in := format.Input{Argv: []string{"rebar3", "as", "test", "eunit"}, Stdout: strings.NewReader(raw), ExitCode: code}
	if _, err := New().Aggressive(context.Background(), in); err != nil {
		t.Errorf("as test eunit: %v", err)
	}
}
