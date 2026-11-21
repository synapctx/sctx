package deno

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func render(t *testing.T, f *Formatter, argv []string, file string, exit int) (format.Rendered, int, error) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: exit,
	})
	return got, len(raw), err
}

func TestRealSamples(t *testing.T) {
	failWant := []string{
		"deno test: FAILED | 3 passed | 3 failed | 1 ignored",
		"FAIL adds wrong — ./a_test.ts:3:6", "AssertionError: Values are not equal.",
		"-   3", "+   4", "at ./a_test.ts:3:33",
		"FAIL obj — ./a_test.ts:4:6", "-       2,", "+       3,",
		"FAIL throws — ./a_test.ts:6:6", "TypeError: kaboom", "at ./a_test.ts:6:35",
		"error: Test failed", "…+",
	}
	failAbsent := []string{"\x1b", "jsr.io", "Download", "... ok", "FAILURES", "throw new AssertionError"}
	tests := []struct {
		name   string
		f      *Formatter
		file   string
		exit   int
		want   []string
		absent []string
	}{
		{"pass", NewTest(), "test-pass.txt", 0, []string{"deno test: ok | 2 passed | 0 failed (14ms)"}, []string{"…+", "Check"}},
		{"fail with colour and downloads", NewTest(), "test-fail.txt", 1, failWant, failAbsent},
		{"fail cached", NewTest(), "test-fail-cached.txt", 1, failWant, failAbsent},
		{"fail NO_COLOR", NewTest(), "test-fail-nocolor.txt", 1, failWant, failAbsent},
		{"test output kept", NewTest(), "test-output.txt", 0, []string{"output of log:", "hello from test"}, []string{"-----"}},
		{"lint", NewLint(), "lint-fail.txt", 1, []string{
			"deno lint: 7 problems", "lint.ts", "1:5 no-unused-vars: `unused` is never used",
			"3:22 no-debugger", "a_test.ts", "Found 7 problems", "…+",
		}, []string{"/repo/", "docs.deno.com", "-->"}},
		{"check", NewCheck(), "check-fail.txt", 1, []string{
			"deno check: 2 problems", "bad.ts", "1:7 TS2322: Type 'string' is not assignable to type 'number'.",
			"1:30 TS2322", "error: Type checking failed.",
		}, []string{"/repo/", "^"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, raw, err := render(t, tc.f, []string{"deno", tc.f.sub}, tc.file, tc.exit)
			if err != nil {
				t.Fatal(err)
			}
			body := string(got.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Errorf("unexpected %q in:\n%s", a, body)
				}
			}
			if len(got.Body) >= raw {
				t.Errorf("not smaller: %d >= %d", len(got.Body), raw)
			}
			if strings.Contains(body, "…+") != got.Elided {
				t.Errorf("Elided=%v disagrees with markers", got.Elided)
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	tests := []struct {
		name string
		f    *Formatter
		argv []string
		file string
		out  string
		exit int
	}{
		{"clean lint has nothing to render", NewLint(), []string{"deno", "lint"}, "lint-pass.txt", "", 0},
		{"clean check", NewCheck(), []string{"deno", "check"}, "check-pass.txt", "", 0},
		{"watch mode", NewTest(), []string{"deno", "test", "--watch"}, "test-fail.txt", "", 1},
		{"json lint", NewLint(), []string{"deno", "lint", "--json"}, "lint-fail.txt", "", 1},
		{"junit reporter", NewTest(), []string{"deno", "test", "--reporter=junit"}, "test-fail.txt", "", 1},
		{"unknown text", NewTest(), []string{"deno", "test"}, "", "hello\n", 0},
		{"non-zero exit without a failing test", NewTest(), []string{"deno", "test"}, "", "ok | 1 passed | 0 failed (1ms)\n", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.out)
			if tc.file != "" {
				var err error
				if raw, err = os.ReadFile("testdata/" + tc.file); err != nil {
					t.Fatal(err)
				}
			}
			_, err := tc.f.Aggressive(context.Background(), format.Input{
				Argv: tc.argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: tc.exit,
			})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

func TestDescriptors(t *testing.T) {
	for _, f := range All() {
		d := f.Descriptor()
		if d.Command != "deno" || len(d.Subcommands) != 1 {
			t.Errorf("descriptor %+v", d)
		}
	}
}
