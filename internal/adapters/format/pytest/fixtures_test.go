package pytest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Captures from the real pytest 9 binary against a project with a failing
// assert, a failing parametrized case, an unexpected exception, an xfail, a
// skip and a unittest.TestCase — default, -v, -q and -rA output.
func TestRealFixtures(t *testing.T) {
	failing := []string{
		"___ test_fail ___", "> ", "assert add(1, 2) == 4", "E       assert 3 == 4", "+  where 3 = add(1, 2)",
		"tests/test_core.py:8: AssertionError",
		"___ test_param[2-2-5] ___", "assert add(a, b) == e", "E       assert 4 == 5",
		"___ test_err ___", `raise ValueError("boom")`, "E       ValueError: boom",
		"FAILED tests/test_core.py::test_fail - assert 3 == 4",
		"FAILED tests/test_core.py::test_param[2-2-5] - assert 4 == 5",
		"FAILED tests/test_core.py::test_err - ValueError: boom",
		"3 failed, 3 passed, 1 skipped, 1 xfailed in",
	}
	tests := []struct {
		name, fixture string
		keep, drop    []string
	}{
		{"default run", "pytest_fail", append([]string{"FAILED tests/test_unit.py::T::test_b - AssertionError: 1 != 2",
			"E   RuntimeError: x", "5 failed, 4 passed, 1 skipped, 1 xfailed in"}, failing[:len(failing)-1]...),
			[]string{"platform darwin", "rootdir", "plugins:", "tests/test_unit.py .FF", "____________"}},
		{"-v run", "pytest_v", failing, []string{"PASSED  ", "test_ok PASSED", "platform darwin"}},
		{"-q run", "pytest_q", failing, []string{"platform darwin"}},
		{"-rA collapses the passed rows and keeps skip and xfail", "pytest_rA",
			append([]string{"PASSED ×3", "SKIPPED [1] tests/test_core.py:18: no", "XFAIL tests/test_core.py::test_xf"}, failing...),
			[]string{"PASSED tests/"}},
		{"all green", "pytest_pass", []string{"1 passed, 1 skipped, 5 deselected, 1 xfailed in"}, []string{"platform darwin"}},
		{"collection error keeps the user's frame and the exception", "pytest_collect",
			[]string{"ERROR collecting tests/test_broken.py", "tests/test_broken.py:1: in <module>", "import nonexistent_mod",
				"E   ModuleNotFoundError: No module named 'nonexistent_mod'", "ERROR tests/test_broken.py",
				"Interrupted: 1 error during collection", "1 error in"},
			[]string{"importlib", "_gcd_import"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := load(t, tt.fixture)
			in := format.Input{Argv: []string{"pytest"}, Stdout: strings.NewReader(raw), ExitCode: 1}
			out, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			t.Logf("raw=%d render=%d", len(raw), len(body))
			for _, k := range tt.keep {
				if !strings.Contains(body, k) {
					t.Errorf("missing %q", k)
				}
			}
			for _, d := range tt.drop {
				if strings.Contains(body, d) {
					t.Errorf("unexpected %q", d)
				}
			}
			if len(body) >= len(raw) {
				t.Errorf("render %d not smaller than raw %d", len(body), len(raw))
			}
		})
	}
}

// unittest's "=====" separator must not read as a pytest banner: the old
// check rendered it as "failed (no diagnostic output captured)", dropping the
// whole report.
func TestUnittestOutputIsNotPytest(t *testing.T) {
	in := func() format.Input {
		return format.Input{Argv: []string{"pytest"}, Stdout: strings.NewReader(load(t, "unittest")), ExitCode: 1}
	}
	if _, err := New().Aggressive(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err = %v, want ErrTierInapplicable", err)
	}
	out, err := New().Relaxed(context.Background(), in())
	if err != nil || !strings.Contains(string(out.Body), "AssertionError: 1 != 2") {
		t.Fatalf("relaxed lost the failure: err=%v body=%q", err, out.Body)
	}
}

func TestUnittestFixtures(t *testing.T) {
	getwd = func() (string, error) { return "/work/proj", nil }
	t.Cleanup(func() { getwd = os.Getwd })
	tests := []struct {
		name, fixture string
		keep, drop    []string
	}{
		{"failures and errors", "unittest", []string{"ERROR: test_c (tests.test_unit.T.test_c)",
			`tests/test_unit.py:5 in test_c: def test_c(self): raise RuntimeError("x")`, "RuntimeError: x",
			"FAIL: test_b (tests.test_unit.T.test_b)", "tests/test_unit.py:4 in test_b", "AssertionError: 1 != 2",
			"Ran 3 tests in 0.002s", "FAILED (failures=1, errors=1)"}, []string{"^^^^", "~~~~", "======", "------", "/work/proj"}},
		{"-v drops the ok rows with a count", "unittest_v", []string{"test_b (tests.test_unit.T.test_b) ... FAIL",
			"test_c (tests.test_unit.T.test_c) ... ERROR", "[1 passing rows omitted]", "FAILED (failures=1, errors=1)"},
			[]string{"test_a (tests.test_unit.T.test_a) ... ok"}},
		{"all green", "unittest_ok", []string{"Ran 3 tests in 0.002s", "OK (skipped=1, expected failures=1)"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := load(t, tt.fixture)
			// unittest writes its report to stderr.
			out, err := NewUnittest().Aggressive(context.Background(), format.Input{Argv: []string{"python", "-m", "unittest"}, Stderr: strings.NewReader(raw), ExitCode: 1})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
			if !out.FoldStderr {
				t.Error("stderr consumed but FoldStderr false")
			}
			for _, k := range tt.keep {
				if !strings.Contains(body, k) {
					t.Errorf("missing %q", k)
				}
			}
			for _, d := range tt.drop {
				if strings.Contains(body, d) {
					t.Errorf("unexpected %q", d)
				}
			}
		})
	}
}
