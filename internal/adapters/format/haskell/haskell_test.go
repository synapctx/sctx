package haskell

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

// Fixtures were captured from elixir:1.17 (haskell:9.10 (cabal 3.14, GHC 9.10.3, hspec 2.11).
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

func forCmd(c string) *Formatter { return &Formatter{command: c} }

func TestRendersKeepErrorSignal(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		argv []string
		keep []string
		drop []string
	}{
		{"hs-build-warn", "cabal", []string{"cabal", "build", "lib:demo"},
			[]string{"src/Demo.hs:3:1: warning: [GHC-66111] [-Wunused-imports]", "The import of ‘Data.List’ is redundant", "src/Demo.hs:12:1: warning: [GHC-40910] [-Wunused-top-binds]",
				"Defined but not used: ‘helper’", "src/Demo.hs:12:20: warning: [GHC-18042] [-Wtype-defaults]", "• Defaulting the type variable ‘a0’ to type ‘Integer’"},
			[]string{"import Data.List (sort)", "Resolving dependencies", "Preprocessing library", "[1 of 1] Compiling", "^^^^^^"}},
		{"hs-build-err", "cabal", []string{"cabal", "build"},
			[]string{"src/Demo.hs:4:15: error: [GHC-88464]", "Variable not in scope: c :: Int", "src/Demo.hs:10:7: error: [GHC-88464]", "Variable not in scope: undefinedThing :: Int -> Int",
				"Error: [Cabal-7125]", "Failed to build demo-0.1.0.0."},
			[]string{"add a b = a + c", "f x = undefinedThing x", "Build profile"}},
		{"hs-test-fail", "cabal", []string{"cabal", "test"},
			[]string{"Failures:", "test/Spec.hs:8:31:", "1) add adds wrong", "expected: 4", "but got: 3", "expected: \"hi bob\"", "To rerun use: --match \"/add/adds wrong/\"", "4 examples, 3 failures",
				"Test suite spec: FAIL", "0 of 1 test suites (0 of 1 test cases) passed.", "Error: [Cabal-7125]", "Tests failed for test:spec from demo-0.1.0.0."},
			[]string{"requires download & build", "Downloading  colour", "adds [✔]", "Test suite spec: RUNNING", "Completed    hspec-2.11.18 (lib)"}},
		{"hs-test-ok", "cabal", []string{"cabal", "test"},
			[]string{"2 examples, 0 failures", "Test suite spec: PASS", "1 of 1 test suites (1 of 1 test cases) passed."}, []string{"adds more [✔]", "Downloading"}},
		{"hs-ghc-err", "ghc", []string{"ghc", "-Wall", "ghcbad.hs"},
			[]string{"ghcbad.hs:2:18: error: [GHC-39999]", "• No instance for ‘Num String’ arising from the literal ‘1’", "In the expression: putStrLn (1 + \"x\")"},
			[]string{"[1 of 2] Compiling", "main = putStrLn (1 + \"x\")"}},
		{"hs-ghc-warn", "ghc", []string{"ghc", "-Wall", "-fforce-recomp", "src/Demo.hs"},
			[]string{"src/Demo.hs:3:1: warning: [GHC-66111] [-Wunused-imports]", "Defined but not used: ‘y’"}, []string{"helper x = let y = 2 in x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
			out, err := forCmd(tc.cmd).Aggressive(context.Background(), in)
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
			// Every diagnostic header and every Failures entry survives.
			for _, l := range strings.Split(raw, "\n") {
				if (headerRE.MatchString(l) || strings.HasPrefix(l, "Error:") || strings.Contains(l, "To rerun use")) && !strings.Contains(body, strings.TrimSpace(l)) {
					t.Errorf("signal lost: %q", l)
				}
			}
			golden(t, tc.name, out.Body)

			rel, err := forCmd(tc.cmd).Relaxed(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(rel.Body), "…+") {
				t.Error("relaxed elision not marked")
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	raw, code := load(t, "hs-test-fail")
	for _, tc := range []struct {
		cmd  string
		argv []string
	}{
		{"cabal", []string{"cabal"}}, {"cabal", []string{"cabal", "run"}}, {"cabal", []string{"cabal", "repl"}}, {"cabal", []string{"cabal", "update"}},
		{"cabal", []string{"cabal", "build", "--help"}}, {"ghc", []string{"ghc"}}, {"ghc", []string{"ghc", "--version"}}, {"ghc", []string{"ghc", "-e", "1+1"}},
		{"ghc", []string{"ghc", "--interactive"}}, {"ghc", []string{"ghc", "-ddump-simpl", "a.hs"}},
	} {
		in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := forCmd(tc.cmd).Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", tc.argv, err)
		}
	}
}

func TestUpToDateDeclines(t *testing.T) {
	raw, code := load(t, "hs-build-noop")
	in := format.Input{Argv: []string{"cabal", "build"}, Stdout: strings.NewReader(raw), ExitCode: code}
	if _, err := forCmd("cabal").Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("got %v", err)
	}
}

func TestAllCoversPrograms(t *testing.T) {
	got := map[string]bool{}
	for _, f := range All() {
		got[f.Descriptor().Command] = true
	}
	if !got["cabal"] || !got["ghc"] {
		t.Errorf("got %v", got)
	}
}
