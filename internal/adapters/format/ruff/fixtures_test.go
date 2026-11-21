package ruff

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

// fixture loads a capture taken from the real ruff binary (stdout and stderr
// merged, paths scrubbed) and the exit code it returned.
func fixture(t *testing.T, name string) (string, int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join("testdata", name+".exit"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(code)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), n
}

func TestRealFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		argv    []string
		// keep must appear in the aggressive body; none of drop may.
		keep []string
		drop []string
		// declines: both tiers return ErrTierInapplicable.
		declines bool
	}{
		{
			name: "check full format keeps every diagnostic and location", fixture: "ruff_check",
			argv: []string{"ruff", "check", "."},
			keep: []string{
				"pkg/core.py — 7 issues", "L1:8 F401 `os` imported but unused [*]",
				"L19:1 F821 Undefined name `undefined_fn`", "L3:1 UP035", "tests/test_unit.py — 1 issues",
				"14 issues in 5 files", "12 fixable", "snippets omitted",
			},
			drop: []string{"-->", "help:", "fixable with `"},
		},
		{
			name: "check concise", fixture: "ruff_concise",
			argv: []string{"ruff", "check", "--output-format=concise", "."},
			keep: []string{"L19:1 F821 Undefined name `undefined_fn`", "14 issues in 5 files"},
		},
		{
			name: "clean", fixture: "ruff_clean", argv: []string{"ruff", "check", "."},
			keep: []string{"All checks passed!"},
		},
		{
			name: "syntax errors keep every parse error", fixture: "ruff_syntax",
			argv: []string{"ruff", "check", "."},
			keep: []string{"invalid-syntax Expected an expression", "L1:7", "L3:5", "3 issues in 1 files"},
		},
		{
			name: "--fix keeps the fixed/remaining outcome and the unfixable diagnostic", fixture: "ruff_fix",
			argv: []string{"ruff", "check", "--fix", "."},
			keep: []string{"F821 Undefined name `undefined_fn`", "11 fixed, 1 remaining"},
		},
		{
			name: "format --check new shape lists files", fixture: "ruff_fmtcheck",
			argv: []string{"ruff", "format", "--check", "."},
			keep: []string{"pkg/core.py", "tests/test_unit.py", "5 files would be reformatted, 1 file already formatted"},
			drop: []string{"-->", "def add"},
		},
		{
			name: "format clean", fixture: "ruff_fmt_clean", argv: []string{"ruff", "format", "--check", "."},
			keep: []string{"1 file already formatted"},
		},
		{
			name: "format applied", fixture: "ruff_format_apply", argv: []string{"ruff", "format", "."},
			keep: []string{"5 files reformatted, 2 files left unchanged"},
		},
		{name: "format --diff is the user's data", fixture: "ruff_fmtdiff",
			argv: []string{"ruff", "format", "--diff", "."}, declines: true},
		{name: "--statistics is the user's data", fixture: "ruff_stat",
			argv: []string{"ruff", "check", "--statistics", "."}, declines: true},
	}
	f := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, exit := fixture(t, tt.fixture)
			in := func() format.Input {
				return format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw), ExitCode: exit}
			}
			if tt.declines {
				if _, err := f.Aggressive(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
					t.Errorf("Aggressive err = %v, want ErrTierInapplicable", err)
				}
				if _, err := f.Relaxed(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
					t.Errorf("Relaxed err = %v, want ErrTierInapplicable", err)
				}
				return
			}
			out, err := f.Aggressive(context.Background(), in())
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
			}
			body := string(out.Body)
			t.Logf("raw=%d aggressive=%d\n%s", len(raw), len(body), body)
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
			if len(body) >= len(raw) && len(raw) > 60 {
				t.Errorf("render (%d) not smaller than raw (%d)", len(body), len(raw))
			}
			rel, err := f.Relaxed(context.Background(), in())
			if err != nil {
				t.Fatalf("Relaxed: %v", err)
			}
			t.Logf("relaxed=%d", len(rel.Body))
			if exit != 0 && len(rel.Body) == 0 {
				t.Error("relaxed lost all signal on non-zero exit")
			}
		})
	}
}
