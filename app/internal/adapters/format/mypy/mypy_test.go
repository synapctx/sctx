package mypy

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "mypy" {
		t.Errorf("Command = %q, want %q", got.Command, "mypy")
	}
	if len(got.Subcommands) != 0 {
		t.Errorf("Subcommands = %v, want none", got.Subcommands)
	}
}

const cleanRunFixture = "Success: no issues found in 5 source files\n"

func TestAggressiveCleanRun(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"mypy", "."},
		Stdout: strings.NewReader(cleanRunFixture),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if strings.TrimRight(string(out.Body), "\n") != "Success: no issues found in 5 source files" {
		t.Errorf("body = %q", out.Body)
	}
}

const multiErrorFixture = `src/foo.py:10: error: Incompatible types in assignment (expression has type "int", variable has type "str")  [assignment]
src/foo.py:10: note: See https://mypy.readthedocs.io/en/stable/common_issues.html
src/foo.py:10: note: for more details
src/foo.py:10: note: and even more context
src/foo.py:22:5: error: Argument 1 to "helper" has incompatible type "int"; expected "str"  [arg-type]
src/bar.py:5: error: Name "undefined_var" is not defined  [name-defined]
src/bar.py:9: warning: unused "type: ignore" comment
Found 4 errors in 2 files (checked 5 source files)
`

func TestAggressiveMultipleErrorsWithNotes(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"mypy", "."},
		Stdout:   strings.NewReader(multiErrorFixture),
		ExitCode: 1,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v, want nil (exit 1 is the normal errors-found case)", err)
	}
	body := string(out.Body)

	if !strings.Contains(body, "src/foo.py — 2 diagnostics") {
		t.Errorf("missing file group for foo.py: %q", body)
	}
	if !strings.Contains(body, "src/bar.py — 2 diagnostics") {
		t.Errorf("missing file group for bar.py: %q", body)
	}
	if !strings.Contains(body, `L10 error: Incompatible types in assignment (expression has type "int", variable has type "str") [assignment]`) {
		t.Errorf("missing error line: %q", body)
	}
	if !strings.Contains(body, "note: See https://mypy.readthedocs.io/en/stable/common_issues.html") {
		t.Errorf("missing first note: %q", body)
	}
	if !strings.Contains(body, "note: for more details") {
		t.Errorf("missing second note: %q", body)
	}
	if strings.Contains(body, "and even more context") {
		t.Errorf("third note should have been collapsed behind a marker: %q", body)
	}
	if !strings.Contains(body, "…+1 notes") {
		t.Errorf("missing notes-elided marker: %q", body)
	}
	if !strings.Contains(body, "L22:5 error:") {
		t.Errorf("missing column-qualified location: %q", body)
	}
	if !strings.Contains(body, "L9 warning: unused") {
		t.Errorf("missing warning severity: %q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "Found 4 errors in 2 files (checked 5 source files)") {
		t.Errorf("missing final summary: %q", body)
	}
}

func TestAggressiveNonMypyOutputInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"mypy", "."},
		Stdout: strings.NewReader("just some random program output\nwith multiple lines\nand no diagnostics\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveEmptyOutputInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"mypy", "."},
		Stdout: strings.NewReader(""),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestRelaxedKeepsDiagnosticsDropsNoise(t *testing.T) {
	f := New()
	in := format.Input{
		Argv: []string{"mypy", "."},
		Stdout: strings.NewReader(strings.Join([]string{
			"src/foo.py:10: error: Incompatible types in assignment  [assignment]",
			"",
			"src/foo.py:10: note: see docs",
			"src/foo.py:10: note: see docs",
			"Found 1 error in 1 file (checked 5 source files)",
		}, "\n")),
	}
	out, err := f.Relaxed(context.Background(), in)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	body := string(out.Body)
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (dedup + drop blank), got %d: %q", len(lines), lines)
	}
	if !strings.Contains(body, "error: Incompatible types") {
		t.Errorf("missing error line: %q", body)
	}
	if !strings.Contains(body, "Found 1 error in 1 file") {
		t.Errorf("missing summary: %q", body)
	}
}

func TestRelaxedEmptyInputInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"mypy", "."},
		Stdout: strings.NewReader(""),
	}
	if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
