package scala

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

// Fixtures were captured from elixir:1.17 (sbt 1.10.5, Scala 3.5.2 / 2.13.14.
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
		{"sc-compile-warn", []string{"sbt", "compile"},
			[]string{"[warn] -- [E029] Pattern Match Exhaustivity Warning: /work/proj/src/main/scala/Demo.scala:3:41", "[warn]   |                                       match may not be exhaustive.",
				"It would fail on pattern case: None", "[warn] one warning found", "[success] Total time: 8 s"},
			[]string{"welcome to sbt", "loading settings", "def unusedMatch", "longer explanation"}},
		{"sc-compile-err", []string{"sbt", "compile"},
			[]string{"[error] -- [E006] Not Found Error: /work/proj/src/main/scala/Demo.scala:2:37", "Not found: c", "Found:    (42 : Int)", "Required: String", "Not found: undefinedThing",
				"[error] three errors found", "[error] (Compile / compileIncremental) Compilation failed", "[error] Total time: 5 s"},
			[]string{"def add(a: Int", "def s: String = 42", "welcome"}},
		{"sc2-compile-err", []string{"sbt", "compile"},
			[]string{"Demo.scala:2:38: not found: value c", "Demo.scala:3:19: type mismatch;", "[error]  found   : Int(42)", "[error]  required: String",
				"[warn] /work/proj/src/main/scala/Demo.scala:4:35: local val y in method unused is never used", "[error] two errors found"},
			[]string{"def add(a: Int, b: Int): Int = a + c", "def s: String = 42", "                                     ^"}},
		{"sc-test-fail", []string{"sbt", "test"},
			[]string{"DemoSpec:", "- adds wrong *** FAILED ***", "3 did not equal 4 (DemoSpec.scala:4)", "- boom *** FAILED ***", "java.lang.IllegalStateException: kaboom",
				"DemoMunit:", "==> X DemoMunit.munit fail", "values are not the same", "Tests: succeeded 1, failed 2", "[error] Failed: Total 5, Failed 3, Errors 0, Passed 2",
				"[error] \tDemoMunit", "[error] \tDemoSpec", "[error] (Test / test) sbt.TestsFailedException: Tests unsuccessful"},
			[]string{"- adds\n", "+ munit ok", "org.scalatest.OutcomeOf", "Run completed in"}},
		{"sc-test-ok", []string{"sbt", "test"},
			[]string{"Tests: succeeded 2, failed 0", "All tests passed.", "[success] Total time: 7 s"},
			[]string{"- adds", "Suites: completed"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			raw = ansiRE.ReplaceAllString(raw, "")
			full, _ := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(full), ExitCode: code}
			out, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(full) {
				t.Errorf("render %d >= raw %d", len(body), len(full))
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
			// Every error-level line of the raw output survives.
			for _, l := range strings.Split(raw, "\n") {
				if strings.HasPrefix(l, "[error] ") && strings.TrimSpace(l[8:]) != "" && code != 0 {
					m := strings.TrimSpace(l[8:])
					rest := strings.TrimRight(l[7:], " ")
					if srcRE.MatchString(m) || caret3RE.MatchString(rest) || gutterRE.MatchString(rest) || explainRE.MatchString(m) || caret2RE.MatchString(m) {
						continue
					}
					if strings.HasPrefix(m, "def ") {
						continue
					}
					if !strings.Contains(body, m) {
						t.Errorf("error line lost: %q", l)
					}
				}
			}
			golden(t, tc.name, out.Body)

			rel, err := New().Relaxed(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(full), ExitCode: code})
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
	raw, code := load(t, "sc-test-fail")
	for _, argv := range [][]string{
		{"sbt"}, {"sbt", "~compile"}, {"sbt", "~test"}, {"sbt", "run"}, {"sbt", "console"}, {"sbt", "shell"},
		{"sbt", "--client", "compile"}, {"sbt", "compile", "run"}, {"sbt", "show compile:fullClasspath"},
	} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}

func TestQuotedTaskWithArgsIsSupported(t *testing.T) {
	raw, code := load(t, "sc-test-fail")
	in := format.Input{Argv: []string{"sbt", "testOnly DemoSpec"}, Stdout: strings.NewReader(raw), ExitCode: code}
	if _, err := New().Aggressive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}
