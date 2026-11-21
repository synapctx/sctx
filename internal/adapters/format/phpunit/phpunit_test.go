package phpunit

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

// load reads a fixture captured from a real PHPUnit 11.5 run.
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
		{"pass", []string{"vendor/bin/phpunit"}, []string{"Tests: 3, Assertions: 2, Skipped: 1."}},
		{"fail", []string{"vendor/bin/phpunit"}, []string{
			"1 error:", "1) BadTest::testDivZero", "DivisionByZeroError: Division by zero", "src/Calc.php:5", "tests/BadTest.php:6",
			"3 failures:", "Failed asserting that 3 is identical to 4.", "tests/BadTest.php:5",
			"Failed asserting that two arrays are equal.", "-        2 => 3", "+        2 => 4", "tests/BadTest.php:7",
			"Failed asserting that two strings are identical.", "-line2", "+lineX", "tests/BadTest.php:8",
			"1 risky test:", "BadTest::testRisky", "This test did not perform any assertions", "tests/BadTest.php:10",
			"ERRORS! Tests: 9, Assertions: 6, Errors: 1, Failures: 3, Skipped: 1, Risky: 1.",
		}},
		{"testdox", []string{"vendor/bin/phpunit", "--testdox"}, []string{"✘ Add wrong", "Failed asserting that 3 is identical to 4.", "src/Calc.php:5", "ERRORS! Tests: 6,"}},
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
			body := string(r.Body)
			for _, m := range tt.must {
				if !strings.Contains(body, m) {
					t.Errorf("lost %q", m)
				}
			}
			if strings.Contains(body, "/app/") {
				t.Error("absolute project path survived")
			}
			golden(t, tt.name, r.Body)
		})
	}
}

func TestOKSummary(t *testing.T) {
	r, err := New().Aggressive(context.Background(), input([]string{"phpunit"},
		"PHPUnit 10.5.0 by Sebastian Bergmann and contributors.\n\nRuntime:       PHP 8.3.0\n\n...   3 / 3 (100%)\n\nTime: 00:00.003, Memory: 6.00 MB\n\nOK (3 tests, 3 assertions)\n", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "OK (3 tests, 3 assertions)") || strings.Contains(string(r.Body), "Runtime") {
		t.Errorf("body = %q", r.Body)
	}
}

func TestDeclines(t *testing.T) {
	errOut, code := load(t, "error")
	tests := []struct {
		name string
		argv []string
		out  string
	}{
		{"startup error without summary", []string{"phpunit"}, errOut},
		{"teamcity", []string{"phpunit", "--teamcity"}, "##teamcity[testSuiteStarted]\nTests: 1, Assertions: 1\n"},
		{"summary reports failures we cannot find", []string{"phpunit"}, "F\n\nFAILURES!\nTests: 1, Assertions: 1, Failures: 1.\n"},
		{"empty", []string{"phpunit"}, ""},
	}
	_ = code
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New().Aggressive(context.Background(), input(tt.argv, tt.out, 2)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestRelaxedKeepsStartupError(t *testing.T) {
	raw, code := load(t, "error")
	r, err := New().Relaxed(context.Background(), input([]string{"phpunit", "/nonexistent"}, raw, code))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), `Test file "/nonexistent" not found`) {
		t.Errorf("lost startup error: %q", r.Body)
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"phpunit", "--teamcity"}, "x\n", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("teamcity relaxed err = %v", err)
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"phpunit"}, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("empty relaxed err = %v", err)
	}
}

func TestStderrFolded(t *testing.T) {
	raw, code := load(t, "pass")
	in := input([]string{"phpunit"}, raw, code)
	in.Stderr = strings.NewReader("PHP Warning: x\n")
	r, err := New().Aggressive(context.Background(), in)
	if err != nil || !r.FoldStderr || !strings.Contains(string(r.Body), "PHP Warning: x") {
		t.Fatalf("stderr not folded: %v %q", err, r.Body)
	}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "phpunit" {
		t.Fatal("wrong command")
	}
}
