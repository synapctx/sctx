package zig

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

// Fixtures were captured from elixir:1.17 (alpine:3.20 (zig 0.12.0).
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
		{"zg-test-fail", []string{"zig", "build", "test"},
			[]string{"+- run test 2/4 passed, 2 failed", "error: 'main.test.adds wrong' failed: expected 4, found 3", "/work/proj/src/main.zig:5:21: 0x104e887 in test.adds wrong (test)",
				"error: 'main.test.slices' failed:", "First difference occurs on line 1:", "hellp", "error: while executing test 'main.test.ok2'",
				"Build Summary: 1/3 steps succeeded; 1 failed; 2/4 tests passed; 2 failed", "error: the following build command failed with exit code 1:"},
			[]string{"std/testing.zig", "return error.TestExpectedEqual;", "test transitive failure", "--listen=-", "--seed"}},
		{"zg-test-direct", []string{"zig", "test", "src/main.zig"},
			[]string{"2/4 main.test.adds wrong... expected 4, found 3", "FAIL (TestExpectedEqual)", "3/4 main.test.slices...", "2 passed; 0 skipped; 2 failed.", "/work/proj/src/main.zig:6:17: 0x104f5ef in test.slices (test)"},
			[]string{"1/4 main.test.adds... OK", "4/4 main.test.ok2... OK", "std/testing.zig"}},
		{"zg-build-err", []string{"zig", "build"},
			[]string{"src/main.zig:2:45: error: use of undeclared identifier 'c'", "src/main.zig:6:9: error: unused local variable", "src/main.zig:4:11: error: unused local constant",
				"error: the following command failed with 3 compilation errors:", "Build Summary: 0/3 steps succeeded; 1 failed", "error: the following build command failed with exit code 1:"},
			[]string{"--cache-dir", "install transitive failure"}},
		{"zg-test-ok", []string{"zig", "build", "test", "--summary", "all"},
			[]string{"Build Summary: 3/3 steps succeeded; 1/1 tests passed"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
			out, err := New().Aggressive(context.Background(), in)
			if tc.name == "zg-test-ok" {
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
			// Every compiler/test error line survives on a non-zero exit.
			for _, l := range strings.Split(raw, "\n") {
				if code != 0 && (strings.Contains(l, ": error: ") || strings.HasPrefix(l, "error: ")) && !strings.Contains(body, l) {
					t.Errorf("error line lost: %q", l)
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
	raw, code := load(t, "zg-test-fail")
	for _, argv := range [][]string{{"zig"}, {"zig", "run", "x.zig"}, {"zig", "build", "run"}, {"zig", "build", "--watch"}, {"zig", "fmt", "."}, {"zig", "version"}, {"zig", "build", "--help"}} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}
