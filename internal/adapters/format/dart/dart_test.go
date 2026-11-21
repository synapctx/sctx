package dart

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

// Fixtures were captured from elixir:1.17 (dart:3.5 (Dart 3.5.4).
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
		{"dt-test-fail", []string{"dart", "test"},
			[]string{"test/demo_test.dart: adds wrong [E]", "Expected: <4>", "Actual: <3>", "test/demo_test.dart 8:28  main.<fn>",
				"test/demo_test.dart: greets [E]", "Which: is different.", "Differ at offset 1", "Which: at location [2] is <3> instead of <4>",
				"Bad state: boom", "test/demo_test.dart 11:24  main.<fn>", "+2 -4: Some tests failed.",
				"dart test test/demo_test.dart -p vm --plain-name 'adds wrong'"},
			[]string{"package:matcher", "loading test/demo_test.dart", "chain-stack-traces", "+1 -1: test/demo_test.dart: greets\n"}},
		{"dt-test-ok", []string{"dart", "test"},
			[]string{"+2: All tests passed!"}, []string{"loading", "adds2"}},
		{"dt-analyze-fail", []string{"dart", "analyze"},
			[]string{"error - lib/demo.dart:12:11 - A value of type 'String' can't be assigned to a variable of type 'int'. - invalid_assignment",
				"error - lib/demo.dart:13:3 - The function 'undefinedFn' isn't defined. - undefined_function",
				"error - lib/demo.dart:14:9 - Undefined name 'missing'. - undefined_identifier",
				"warning - lib/demo.dart:1:8 - Unused import: 'dart:io'. - unused_import",
				"warning - lib/demo.dart:7:7 - The value of the local variable 'unused' isn't used. - unused_local_variable", "7 issues found."},
			[]string{"Try changing", "Analyzing dt"}},
		{"dt-pubget", []string{"dart", "pub", "get"},
			[]string{"Changed 49 dependencies!", "14 packages have newer versions incompatible with dependency constraints."},
			[]string{"+ analyzer 7.7.1", "Resolving dependencies"}},
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
			// Every analyzer finding location and every [E] row survives.
			for _, l := range strings.Split(strings.ReplaceAll(ansiRE.ReplaceAllString(raw, ""), "\r", "\n"), "\n") {
				l = strings.TrimSpace(l)
				if findingRE.MatchString(l) {
					loc := strings.SplitN(l, " - ", 3)
					if !strings.Contains(body, loc[1]) {
						t.Errorf("finding lost: %q", l)
					}
				}
				if strings.HasSuffix(l, "[E]") && !strings.Contains(body, l) {
					t.Errorf("failure row lost: %q", l)
				}
			}
			golden(t, tc.name, out.Body)

			rel, err := New().Relaxed(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code})
			if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatal(err)
			}
			if err == nil && !strings.Contains(string(rel.Body), "…+") {
				t.Error("relaxed elision not marked")
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	raw, code := load(t, "dt-test-fail")
	for _, argv := range [][]string{
		{"dart"}, {"dart", "run"}, {"dart", "test", "-r", "json"}, {"dart", "test", "--reporter=json"}, {"dart", "test", "--coverage=cov"},
		{"dart", "analyze", "--format=json"}, {"dart", "format", "."}, {"dart", "pub", "run", "x"}, {"dart", "compile", "exe", "a.dart"},
	} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}
