package gotest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures captured from real `go test`/`go vet` runs against scratch
// packages (see task scratchpad), reproduced verbatim.

const passingTestStdout = "ok  \tfixtures/pkgok/a\t(cached)\n" +
	"ok  \tfixtures/pkgok/b\t(cached)\n" +
	"?   \tfixtures/pkgnotf\t[no test files]\n"

const failingTestStdout = "ok  \tfixtures/pkgok/a\t(cached)\n" +
	"ok  \tfixtures/pkgok/b\t(cached)\n" +
	"--- FAIL: TestDiv (0.00s)\n" +
	"    f_test.go:8: Div(10,2) = 5, want 6\n" +
	"FAIL\n" +
	"FAIL\tfixtures/pkgfail\t0.263s\n" +
	"FAIL\n"

const compileErrStdout = "FAIL\tfixtures/compileerr [build failed]\n" +
	"FAIL\n"

const compileErrStderr = "# fixtures/compileerr [fixtures/compileerr.test]\n" +
	"compileerr/c.go:4:9: cannot use \"not an int\" (untyped string constant) as int value in return statement\n"

const vetFailStderr = "vetbad/v.go:6:14: fmt.Printf format %d has arg \"not an int\" of wrong type string\n"

const downloadNoiseStdout = "go: downloading example.com/foo v1.2.3\n" +
	"go: downloading example.com/bar v0.1.0\n" +
	"go: extracting example.com/foo v1.2.3\n" +
	"ok  \tfixtures/pkgok/a\t0.123s\n"

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
	if got.Command != "go" {
		t.Fatalf("Descriptor().Command = %q, want %q", got.Command, "go")
	}
}

func TestAggressive_Test(t *testing.T) {
	f := New()

	t.Run("all passing collapses to summary and drops per-package noise", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test",
			passingTestStdout, "", 0, 850*time.Millisecond)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if len(rendered.Body) == 0 {
			t.Fatal("Body must not be empty for non-empty input")
		}
		if len(rendered.Body) >= len(passingTestStdout) {
			t.Fatalf("Body (%d bytes) should compress raw input (%d bytes)", len(rendered.Body), len(passingTestStdout))
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "ok: 2 packages") {
			t.Errorf("Body = %q, want a package-count summary", body)
		}
		if strings.Contains(body, "fixtures/pkgok/a") {
			t.Errorf("Body = %q, should not repeat per-package ok lines", body)
		}
		if !strings.Contains(body, "no test files") {
			t.Errorf("Body = %q, want notf count preserved", body)
		}
	})

	t.Run("failure keeps FAIL blocks and drops passing package lines", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test",
			failingTestStdout, "", 1, 263*time.Millisecond)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if len(rendered.Body) == 0 {
			t.Fatal("Body must not be empty for non-empty input")
		}
		for _, want := range []string{
			"--- FAIL: TestDiv",
			"f_test.go:8: Div(10,2) = 5, want 6",
			"FAIL\tfixtures/pkgfail",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Body missing required failure signal %q; got %q", want, body)
			}
		}
		if strings.Contains(body, "fixtures/pkgok/a") || strings.Contains(body, "fixtures/pkgok/b") {
			t.Errorf("Body = %q, should drop passing-package ok lines", body)
		}
		if !strings.Contains(body, "ok: 2 other packages") {
			t.Errorf("Body = %q, want a summarized count of passing packages", body)
		}
	})

	t.Run("compile error on stderr is folded in verbatim", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test",
			compileErrStdout, compileErrStderr, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !rendered.FoldStderr {
			t.Error("FoldStderr = false, want true when stderr carries build errors")
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "cannot use \"not an int\"") {
			t.Errorf("Body = %q, want compile error preserved verbatim", body)
		}
		if !strings.Contains(body, "compileerr/c.go:4:9") {
			t.Errorf("Body = %q, want file:line diagnostic preserved", body)
		}
		if !strings.Contains(body, "FAIL\tfixtures/compileerr") {
			t.Errorf("Body = %q, want FAIL line for the failed package", body)
		}
	})
}

func TestAggressive_BuildVet(t *testing.T) {
	f := New()

	t.Run("empty output on success is tier-inapplicable", func(t *testing.T) {
		in := newInput([]string{"go", "vet", "./..."}, "go vet", "", "", 0, 0)
		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("vet failure folds stderr and keeps file:line diagnostics", func(t *testing.T) {
		in := newInput([]string{"go", "vet", "./..."}, "go vet", "", vetFailStderr, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !rendered.FoldStderr {
			t.Error("FoldStderr = false, want true for a vet failure")
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "vetbad/v.go:6:14") {
			t.Errorf("Body = %q, want the file:line diagnostic preserved", body)
		}
	})

	t.Run("build failure dedupes repeated diagnostics", func(t *testing.T) {
		repeated := vetFailStderr + vetFailStderr
		in := newInput([]string{"go", "build", "./..."}, "go build", "", repeated, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if strings.Count(body, "vetbad/v.go:6:14") != 1 {
			t.Errorf("Body = %q, want the repeated diagnostic deduped to one occurrence", body)
		}
	})
}

func TestAggressive_UnknownSubcommand(t *testing.T) {
	f := New()
	// "go doc" has no aggressive-tier handling; unlike "go list" (now
	// handled, see aggressive_list_tier.go) it must still degrade.
	in := newInput([]string{"go", "doc", "fmt.Println"}, "go doc", "func Println(a ...any) (n int, err error)\n", "", 0, 0)

	_, err := f.Aggressive(context.Background(), in)
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for an unhandled go subcommand", err)
	}
}

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("collapses download noise and duplicate lines, keeps ok summary", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test", downloadNoiseStdout, "", 0, 0)

		rendered, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "downloaded 2 modules") {
			t.Errorf("Body = %q, want download noise collapsed to a count", body)
		}
		if strings.Contains(body, "go: downloading") || strings.Contains(body, "go: extracting") {
			t.Errorf("Body = %q, should drop raw progress noise lines", body)
		}
		if !strings.Contains(body, "fixtures/pkgok/a") {
			t.Errorf("Body = %q, want the ok summary line preserved", body)
		}
	})

	t.Run("collapses repeated identical lines", func(t *testing.T) {
		repeatedLine := "some.go:1:1: repeated warning\n"
		stderr := strings.Repeat(repeatedLine, 4)
		in := newInput([]string{"go", "vet", "./..."}, "go vet", "", stderr, 1, 0)

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

	t.Run("keeps failure signal for an unknown subcommand", func(t *testing.T) {
		in := newInput([]string{"go", "list", "./..."}, "go list",
			"", "go: module lookup error: not found\n", 1, 0)

		rendered, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if !strings.Contains(string(rendered.Body), "module lookup error") {
			t.Errorf("Body = %q, want the error line preserved", string(rendered.Body))
		}
	})

	t.Run("never returns an empty body for non-empty input", func(t *testing.T) {
		in := newInput([]string{"go", "env"}, "go env", "\n\n", "", 0, 0)

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
	in := newInput([]string{"go", "vet", "./..."}, "go vet", "", "", 0, 0)

	if _, err := f.Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for empty output", err)
	}
	if _, err := f.Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable for empty output", err)
	}
}
