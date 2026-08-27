package ruff

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "ruff" {
		t.Errorf("Command = %q, want %q", got.Command, "ruff")
	}
	if len(got.Subcommands) != 0 {
		t.Errorf("Subcommands = %v, want none (dispatch is internal)", got.Subcommands)
	}
}

func TestAggressiveCleanPass(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"ruff", "check", "."},
		Stdout: strings.NewReader("All checks passed!\n"),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if string(out.Body) != "All checks passed!" {
		t.Errorf("Body = %q", out.Body)
	}
}

const sixDiagnosticFixture = `example.py:1:1: F401 [*] ` + "`os`" + ` imported but unused
example.py:3:80: E501 Line too long (92 > 88 characters)
other.py:5:1: F841 [*] Local variable ` + "`x`" + ` is assigned to but never used
other.py:12:5: E711 Comparison to ` + "`None`" + ` should be ` + "`cond is None`" + `
other.py:20:1: F821 Undefined name ` + "`foo`" + `
pkg/mod.py:2:1: D100 Missing docstring in public module
Found 6 errors.
[*] 2 fixable with the ` + "`--fix`" + ` option.
`

func TestAggressiveCheckSixDiagnosticsThreeFiles(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"ruff", "check", "."},
		Stdout:   strings.NewReader(sixDiagnosticFixture),
		ExitCode: 1,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v, want nil (exit 1 is the normal diagnostics-found case)", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "example.py — 2 issues") {
		t.Errorf("missing file group: %q", body)
	}
	if !strings.Contains(body, "other.py — 3 issues") {
		t.Errorf("missing file group: %q", body)
	}
	if !strings.Contains(body, "L1:1 F401") || !strings.Contains(body, "[*]") {
		t.Errorf("missing fixable marker: %q", body)
	}
	if !strings.Contains(body, "L3:80 E501 Line too long (92 > 88 characters)") {
		t.Errorf("missing non-fixable diagnostic: %q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "6 issues in 3 files (2 fixable with --fix)") {
		t.Errorf("missing final summary: %q", body)
	}
}

func TestAggressiveCheckExitTwoDegrades(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"ruff", "check", "."},
		Stdout:   strings.NewReader(""),
		Stderr:   strings.NewReader("error: Failed to parse pyproject.toml\n"),
		ExitCode: 2,
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveCheckEmptyInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"ruff", "check", "."},
		Stdout: strings.NewReader(""),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveFormatReformattedSummary(t *testing.T) {
	f := New()
	in := format.Input{
		Argv: []string{"ruff", "format", "."},
		Stdout: strings.NewReader(
			"Reformatted example.py\n" +
				"Reformatted other.py\n" +
				"2 files reformatted, 3 files left unchanged\n",
		),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "example.py") || !strings.Contains(body, "other.py") {
		t.Errorf("missing per-file lines: %q", body)
	}
	if !strings.HasSuffix(body, "2 files reformatted, 3 files left unchanged") {
		t.Errorf("missing summary: %q", body)
	}
}

func TestAggressiveFormatCapsLongFileList(t *testing.T) {
	f := New()
	var sb strings.Builder
	for i := range 15 {
		fmt.Fprintf(&sb, "Would reformat: file%d.py\n", i)
	}
	sb.WriteString("15 files would be reformatted, 0 files already formatted\n")
	in := format.Input{
		Argv:   []string{"ruff", "format", "--check", "."},
		Stdout: strings.NewReader(sb.String()),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "…+5 more") {
		t.Errorf("missing elision marker: %q", body)
	}
	if !strings.HasSuffix(body, "15 files would be reformatted, 0 files already formatted") {
		t.Errorf("missing summary: %q", body)
	}
}

func TestAggressiveNonRuffOutputInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"ruff", "check", "."},
		Stdout: strings.NewReader("total 24\ndrwxr-xr-x  5 user  staff  160 Jan  1 00:00 .\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestRelaxedDropsBlankAndDedupes(t *testing.T) {
	f := New()
	in := format.Input{
		Argv: []string{"ruff", "check", "."},
		Stdout: strings.NewReader(
			"example.py:1:1: F401 [*] `os` imported but unused\n" +
				"\n" +
				"example.py:1:1: F401 [*] `os` imported but unused\n" +
				"Found 1 error.\n",
		),
	}
	out, err := f.Relaxed(context.Background(), in)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	body := string(out.Body)
	if strings.Count(body, "F401") != 1 {
		t.Errorf("expected dedup of consecutive identical diagnostic, got: %q", body)
	}
	if !strings.Contains(body, "Found 1 error.") {
		t.Errorf("missing summary: %q", body)
	}
}
