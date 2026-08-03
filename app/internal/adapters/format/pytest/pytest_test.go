package pytest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures reproduce real pytest default-mode text output (captured shape
// from `pytest -q` / default runs against scratch test files).

const allPassStdout = "============================= test session starts ==============================\n" +
	"platform darwin -- Python 3.11.4, pytest-7.4.0, pluggy-1.2.0\n" +
	"rootdir: /repo/scratch\n" +
	"collected 3 items\n" +
	"\n" +
	"test_math.py ...                                                       [100%]\n" +
	"\n" +
	"============================== 3 passed in 0.05s ===============================\n"

const someFailedStdout = "============================= test session starts ==============================\n" +
	"platform darwin -- Python 3.11.4, pytest-7.4.0, pluggy-1.2.0\n" +
	"rootdir: /repo/scratch\n" +
	"collected 5 items\n" +
	"\n" +
	"test_math.py ..F..                                                     [100%]\n" +
	"\n" +
	"=================================== FAILURES ===================================\n" +
	"_________________________________ test_divide ___________________________________\n" +
	"\n" +
	"    def test_divide():\n" +
	"        result = divide(10, 2)\n" +
	">       assert result == 6\n" +
	"E       assert 5 == 6\n" +
	"\n" +
	"test_math.py:12: AssertionError\n" +
	"=========================== short test summary info ============================\n" +
	"FAILED test_math.py::test_divide - assert 5 == 6\n" +
	"========================= 1 failed, 4 passed in 0.12s ==========================\n"

const skippedStdout = "============================= test session starts ==============================\n" +
	"platform darwin -- Python 3.11.4, pytest-7.4.0, pluggy-1.2.0\n" +
	"rootdir: /repo/scratch\n" +
	"collected 4 items\n" +
	"\n" +
	"test_math.py ..Fs                                                      [100%]\n" +
	"\n" +
	"=================================== FAILURES ===================================\n" +
	"_________________________________ test_divide ___________________________________\n" +
	"\n" +
	"    def test_divide():\n" +
	">       assert 1 == 2\n" +
	"E       assert 1 == 2\n" +
	"\n" +
	"test_math.py:9: AssertionError\n" +
	"=========================== short test summary info ============================\n" +
	"FAILED test_math.py::test_divide - assert 1 == 2\n" +
	"=================== 1 failed, 2 passed, 1 skipped in 0.08s =====================\n"

const collectionErrorStdout = "============================= test session starts ==============================\n" +
	"platform darwin -- Python 3.11.4, pytest-7.4.0, pluggy-1.2.0\n" +
	"rootdir: /repo/scratch\n" +
	"collected 0 items / 1 error\n" +
	"\n" +
	"==================================== ERRORS =====================================\n" +
	"____________________ ERROR collecting test_broken.py ____________________________\n" +
	"ImportError while importing test module '/repo/scratch/test_broken.py'.\n" +
	"Hint: make sure your test modules/packages have valid Python names.\n" +
	"test_broken.py:1: in <module>\n" +
	"    import nonexistent_module\n" +
	"E   ModuleNotFoundError: No module named 'nonexistent_module'\n" +
	"=========================== short test summary info ============================\n" +
	"ERROR test_broken.py\n" +
	"=============================== 1 error in 0.05s ================================\n"

const notPytestStdout = "Hello world\nsome random build tool output\nBUILD SUCCESSFUL in 2s\n"

func newInput(argv []string, command string, stdout, stderr string, exitCode int, dur time.Duration) format.Input {
	var out, errR *bytes.Reader
	if stdout != "" {
		out = bytes.NewReader([]byte(stdout))
	} else {
		out = bytes.NewReader(nil)
	}
	if stderr != "" {
		errR = bytes.NewReader([]byte(stderr))
	} else {
		errR = bytes.NewReader(nil)
	}
	return format.Input{
		Argv:     argv,
		Command:  command,
		Stdout:   out,
		Stderr:   errR,
		ExitCode: exitCode,
		Duration: dur,
	}
}

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "pytest" {
		t.Fatalf("Descriptor().Command = %q, want %q", got.Command, "pytest")
	}
}

func TestAggressive_AllPass(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest", "-q"}, "pytest", allPassStdout, "", 0, 50*time.Millisecond)

	rendered, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if len(rendered.Body) == 0 {
		t.Fatal("Body must not be empty for non-empty input")
	}
	if len(rendered.Body) >= len(allPassStdout) {
		t.Fatalf("Body (%d bytes) should compress raw input (%d bytes)", len(rendered.Body), len(allPassStdout))
	}
	body := string(rendered.Body)
	if !strings.Contains(body, "3 passed in 0.05s") {
		t.Errorf("Body = %q, want the summary line preserved", body)
	}
	if strings.Contains(body, "test session starts") || strings.Contains(body, "rootdir") {
		t.Errorf("Body = %q, should drop the preamble banner/rootdir noise", body)
	}
	if strings.Contains(body, "test_math.py ...") {
		t.Errorf("Body = %q, should drop the progress dots row", body)
	}
}

func TestAggressive_SomeFailed(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest", "-q"}, "pytest", someFailedStdout, "", 1, 120*time.Millisecond)

	rendered, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(rendered.Body)
	if len(rendered.Body) == 0 {
		t.Fatal("Body must not be empty for non-empty input")
	}
	if len(rendered.Body) >= len(someFailedStdout) {
		t.Fatalf("Body (%d bytes) should compress raw input (%d bytes)", len(rendered.Body), len(someFailedStdout))
	}
	for _, want := range []string{
		"test_divide",
		"E       assert 5 == 6",
		"test_math.py:12: AssertionError",
		"FAILED test_math.py::test_divide - assert 5 == 6",
		"1 failed, 4 passed in 0.12s",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Body missing required failure signal %q; got %q", want, body)
		}
	}
	if strings.Contains(body, "test session starts") || strings.Contains(body, "rootdir") {
		t.Errorf("Body = %q, should drop the preamble banner/rootdir noise", body)
	}
	if strings.Contains(body, "test_math.py ..F..") {
		t.Errorf("Body = %q, should drop the progress dots row", body)
	}
	if strings.Contains(body, "result = divide(10, 2)") {
		t.Errorf("Body = %q, should collapse traceback code context lines", body)
	}
	if !strings.Contains(body, "…+") {
		t.Errorf("Body = %q, want an elision marker for the collapsed traceback", body)
	}
}

func TestAggressive_Skipped(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest", "-q"}, "pytest", skippedStdout, "", 1, 80*time.Millisecond)

	rendered, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(rendered.Body)
	if !strings.Contains(body, "1 failed, 2 passed, 1 skipped in 0.08s") {
		t.Errorf("Body = %q, want the skipped count preserved in the summary", body)
	}
	if !strings.Contains(body, "FAILED test_math.py::test_divide") {
		t.Errorf("Body = %q, want the FAILED node id preserved", body)
	}
}

func TestAggressive_CollectionError(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest", "-q"}, "pytest", collectionErrorStdout, "", 2, 50*time.Millisecond)

	rendered, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(rendered.Body)
	for _, want := range []string{
		"ERROR collecting test_broken.py",
		"ModuleNotFoundError: No module named 'nonexistent_module'",
		"ERROR test_broken.py",
		"1 error in 0.05s",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Body missing required error signal %q; got %q", want, body)
		}
	}
}

func TestAggressive_NotPytest(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest"}, "pytest", notPytestStdout, "", 0, 0)

	_, err := f.Aggressive(context.Background(), in)
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for non-pytest output", err)
	}
}

func TestAggressive_EmptyInput(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest"}, "pytest", "", "", 0, 0)

	_, err := f.Aggressive(context.Background(), in)
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for empty output", err)
	}
}

func TestAggressive_UsageError(t *testing.T) {
	f := New()
	stderr := "ERROR: usage: pytest [options] [file_or_dir] [file_or_dir] [...]\n" +
		"pytest: error: unrecognized arguments: --bogus\n"
	in := newInput([]string{"pytest", "--bogus"}, "pytest", "", stderr, 4, 0)

	_, err := f.Aggressive(context.Background(), in)
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for a usage error with no banners", err)
	}
}

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("collapses progress row, keeps failure and summary signal", func(t *testing.T) {
		in := newInput([]string{"pytest", "-q"}, "pytest", someFailedStdout, "", 1, 0)

		rendered, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "FAILED test_math.py::test_divide") {
			t.Errorf("Body = %q, want the FAILED line preserved", body)
		}
		if !strings.Contains(body, "1 failed, 4 passed in 0.12s") {
			t.Errorf("Body = %q, want the summary line preserved", body)
		}
	})

	t.Run("collapses repeated identical lines", func(t *testing.T) {
		repeatedLine := "E       assert 1 == 2\n"
		stderr := strings.Repeat(repeatedLine, 4)
		in := newInput([]string{"pytest"}, "pytest", "", stderr, 1, 0)

		rendered, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "×4") {
			t.Errorf("Body = %q, want repeated lines collapsed with a ×N count", body)
		}
		if !rendered.FoldStderr {
			t.Error("FoldStderr = false, want true when stderr carried content")
		}
	})

	t.Run("keeps a FAILED progress row despite the [NN%] trailer", func(t *testing.T) {
		in := newInput([]string{"pytest", "-v"}, "pytest",
			"test_math.py::test_divide FAILED                                       [ 50%]\n", "", 1, 0)

		rendered, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if !strings.Contains(string(rendered.Body), "FAILED") {
			t.Errorf("Body = %q, want the FAILED status row preserved", string(rendered.Body))
		}
	})

	t.Run("never returns an empty body for non-empty input", func(t *testing.T) {
		in := newInput([]string{"pytest"}, "pytest", "\n\n", "", 0, 0)

		rendered, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if len(rendered.Body) == 0 {
			t.Fatal("Body must not be empty when raw input was non-empty")
		}
	})
}

func TestEmptyInputIsTierInapplicable(t *testing.T) {
	f := New()
	in := newInput([]string{"pytest"}, "pytest", "", "", 0, 0)

	if _, err := f.Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for empty output", err)
	}
	if _, err := f.Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable for empty output", err)
	}
}
