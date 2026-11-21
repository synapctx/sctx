package jstest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
)

func input(t *testing.T, file string, exit int) (format.Input, int) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return format.Input{Stderr: bytes.NewReader(raw), Stdout: bytes.NewReader(nil), ExitCode: exit, Duration: time.Second}, len(raw)
}

func TestAggressive(t *testing.T) {
	tests := []struct {
		name      string
		formatter *Formatter
		file      string
		exit      int
		want      []string
		absent    []string
	}{
		{
			name: "jest pass collapses to one line", formatter: NewJest(), file: "jest-v30-pass.txt",
			want: []string{"jest: 2 passed, 1 suites, 0.246s"}, absent: []string{"PASS", "…+", "estimated"},
		},
		{
			name: "jest verbose pass drops estimate", formatter: NewJest(), file: "jest-v30-pass-verbose.txt",
			want: []string{"jest: 2 passed, 1 suites, 0.174s"}, absent: []string{"estimated", "✓"},
		},
		{
			name: "jest coverage table survives", formatter: NewJest(), file: "jest-v30-coverage.txt",
			want:   []string{"jest: 2 passed", "% Stmts", "All files |      50", "math.js  |      50"},
			absent: []string{"other lines"},
		},
		{
			name: "jest failures keep diff and first user frame", formatter: NewJest(), file: "jest-v30-fail.txt", exit: 1,
			want: []string{
				"jest: 3 failed, 1 skipped, 3 passed, 3 suites (2 failed), 0.228s",
				"FAIL math › adds wrong — src/math.test.js",
				"Expected: 4", "Received: 3",
				"at src/math.test.js:4:43",
				"FAIL math › div throws — src/math.test.js",
				`Expected substring: "nope"`,
				"at src/math.js:1:62 (…+5 frames)",
				"FAIL math › obj eq",
				"-     4,", "+     3,",
				"FAIL Test suite failed to run — src/broken.test.js",
				"Cannot find module './nonexistent'",
				"code-frame lines",
			},
			absent: []string{"> 4 |", "jest-resolve"},
		},
		{
			name: "vitest pass", formatter: NewVitest(), file: "vitest-v5.1.0-pass.txt",
			want: []string{"vitest: 2215 passed, 185 files, 31.95s"}, absent: []string{"learn more", "…+"},
		},
		{
			name: "vitest failures", formatter: NewVitest(), file: "vitest-v5.1.0-fail.txt", exit: 1,
			want: []string{
				"vitest: 3 failed, 2 passed, 2 files (2 failed), 198ms",
				"FAIL math › adds wrong — src/math.test.ts",
				"AssertionError: expected 3 to be 4",
				"- Expected", "+ Received",
				"at src/math.test.ts:7:46",
				"FAIL math › objects › deep equal — src/math.test.ts",
				`+     3,`,
				"FAIL throws unexpectedly — src/thrower.test.ts",
				"Error: kaboom",
				"at src/thrower.test.ts:2:25 (…+1 frames)",
			},
			absent: []string{"describe('math'", "⎯⎯"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, raw := input(t, tc.file, tc.exit)
			got, err := tc.formatter.Aggressive(context.Background(), in)
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
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
			t.Logf("%s: %d -> %d bytes", tc.file, raw, len(got.Body))
		})
	}
}

func TestDeclines(t *testing.T) {
	tests := []struct {
		name, out string
		exit      int
		f         *Formatter
	}{
		{"not a test run", "hello\nworld\n", 0, NewJest()},
		{"jest shape fed to vitest", "Test Suites: 1 passed, 1 total\nTests:       1 passed, 1 total\n", 0, NewVitest()},
		{"non-zero exit with nothing attributable", "Test Suites: 1 passed, 1 total\nTests:       1 passed, 1 total\nJest: coverage threshold not met\n", 1, NewJest()},
		{"vitest unhandled errors", " Test Files  1 passed (1)\n      Tests  1 passed (1)\n   Duration  1s\n   Errors  1 error\n", 1, NewVitest()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.f.Aggressive(context.Background(), format.Input{
				Stdout: strings.NewReader(tc.out), Stderr: strings.NewReader(""), ExitCode: tc.exit,
			})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

func TestRealJSONReport(t *testing.T) {
	raw, err := os.ReadFile("testdata/jest-v30-json.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewJest().Aggressive(context.Background(), format.Input{
		Stdout: bytes.NewReader(raw), Stderr: strings.NewReader(""), ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(got.Body)
	for _, w := range []string{"FAIL math adds wrong", "FAIL math div throws", "FAIL math obj eq"} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in:\n%s", w, body)
		}
	}
	if len(got.Body) >= len(raw) {
		t.Errorf("not smaller: %d >= %d", len(got.Body), len(raw))
	}
}

func TestJSONReport(t *testing.T) {
	const doc = `{"numTotalTestSuites":2,"numFailedTestSuites":1,"numTotalTests":3,"numPassedTests":2,"numFailedTests":1,"numPendingTests":0,
"testResults":[{"name":"/r/a.test.js","status":"passed","assertionResults":[{"fullName":"a ok","status":"passed","failureMessages":[]}]},
{"name":"/r/b.test.js","status":"failed","assertionResults":[{"fullName":"b adds","status":"failed","failureMessages":["Error: expected 3 to be 4\n    at Object.<anonymous> (/r/b.test.js:5:17)\n    at node_modules/x/y.js:1:1"]}]}]}`
	got, err := NewJest().Aggressive(context.Background(), format.Input{
		Stdout: strings.NewReader(doc), Stderr: strings.NewReader(""), ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"jest: 1 failed, 2 passed, 2 suites (1 failed)", "FAIL b adds — /r/b.test.js", "expected 3 to be 4", "at /r/b.test.js:5:17 (…+1 frames)"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q in:\n%s", w, got.Body)
		}
	}
}
