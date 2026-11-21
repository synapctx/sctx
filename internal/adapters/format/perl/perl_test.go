package perl

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

// Fixtures were captured from elixir:1.17 (perl:5.40 (prove 3.48).
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
		{"pl-prove-fail", []string{"prove", "-l", "t"},
			[]string{"#   Failed test 'adds wrong'", "#   at t/01-add.t line 5.", "#          got: '3'", "#     expected: '4'", "$got->{b}[1] = '2'", "doesn't match '(?^:xyz)'",
				"boom in test at t/04-die.t line 4.", "# Looks like your test exited with 255 just after 1.", "Dubious, test returned 255 (wstat 65280, 0xff00)",
				"Test Summary Report", "t/01-add.t (Wstat: 512 (exited 2) Tests: 3 Failed: 2)", "Failed tests:  1-2, 4", "Parse errors: Bad plan.  You planned 2 tests but ran 1.", "Result: FAIL"},
			[]string{"t/03-ok.t .... ok", "Failed 2/3 subtests", "# Looks like you failed 2 tests of 3."}},
		{"pl-prove-v-fail", []string{"prove", "-lv", "t/02-list.t", "t/03-ok.t"},
			[]string{"not ok 1 - list compare", "not ok 4 - regex", "t/02-list.t ..", "Result: FAIL", "Failed tests:  1-2, 4"},
			[]string{"ok 3 - fine", "ok 7 - t7", "1..12", "t/03-ok.t ...."}},
		{"pl-prove-v-ok", []string{"prove", "-lv", "t/03-ok.t"},
			[]string{"All tests successful.", "Result: PASS", "Files=1, Tests=12"}, []string{"ok 5 - t5"}},
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
			// Every failure row and every diagnostic comment survives.
			for _, l := range strings.Split(raw, "\n") {
				if (strings.HasPrefix(l, "not ok") || strings.HasPrefix(l, "#")) && !strings.Contains(body, strings.TrimRight(l, " ")) &&
					!looksRE.MatchString(l) {
					t.Errorf("signal lost: %q", l)
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
	raw, code := load(t, "pl-prove-fail")
	for _, argv := range [][]string{{"prove", "--help"}, {"prove", "--formatter", "TAP::Formatter::JUnit", "t"}, {"prove", "--archive=x.tgz", "t"}, {"prove", "-V"}} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", argv, err)
		}
	}
}

func TestPassingNonVerboseRunDeclines(t *testing.T) {
	raw, code := load(t, "pl-prove-ok")
	in := format.Input{Argv: []string{"prove", "-l", "t"}, Stdout: strings.NewReader(raw), ExitCode: code}
	out, err := New().Aggressive(context.Background(), in)
	if err == nil && len(out.Body) >= len(raw) {
		t.Errorf("render must be smaller")
	}
	if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatal(err)
	}
}
