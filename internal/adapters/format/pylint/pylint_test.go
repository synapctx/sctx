package pylint

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

func TestRealFixtures(t *testing.T) {
	tests := []struct {
		name, fixture string
		f             *Formatter
		argv          []string
		keep, drop    []string
		decline       bool
	}{
		{name: "pylint groups repeats and keeps every location", fixture: "pylint", f: New(), argv: []string{"pylint", "pkg"},
			keep: []string{"pkg/core.py (", "Missing function or method docstring ×4 @ 5:0 8:0 11:0 14:0",
				"L19:0 E0602 Undefined variable 'undefined_fn'", "L1:0 W0611 Unused import os", "L1:0 C0410 Multiple imports on one line (os, sys)",
				"rated at 0.53/10"},
			drop: []string{"*****", "(unused-import)", "-----"}},
		{name: "pylint syntax error survives", fixture: "pylint_syntax", f: New(), argv: []string{"pylint", "bad.py"},
			keep: []string{"E0001 Parsing failed: 'invalid syntax (bad, line 1)'"}},
		{name: "pylint clean has only the rating", fixture: "pylint_clean", f: New(), argv: []string{"pylint", "m.py"}, decline: true},
		{name: "flake8 groups per file", fixture: "flake8", f: NewFlake8(), argv: []string{"flake8", "pkg"},
			keep: []string{"pkg/core.py (8)", "E302 expected 2 blank lines, found 1 ×4 @ 5:1 8:1 11:1 14:1",
				"L19:1 F821 undefined name 'undefined_fn'", "pkg/ugly.py ("}},
		{name: "flake8 syntax", fixture: "flake8_syntax", f: NewFlake8(), argv: []string{"flake8", "bad.py"},
			keep: []string{"L1:8 E999 SyntaxError: invalid syntax"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := load(t, tt.fixture)
			out, err := tt.f.Aggressive(context.Background(), format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw), ExitCode: 1})
			if tt.decline {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
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

func TestMachineFlagsDecline(t *testing.T) {
	for _, argv := range [][]string{
		{"pylint", "--output-format=json", "x"},
		{"pylint", "--msg-template={path}", "x"},
		{"flake8", "--format=pylint", "x"},
		{"flake8", "--statistics", "x"},
	} {
		in := format.Input{Argv: argv, Stdout: strings.NewReader("a.py:1:1: F401 x\n")}
		if _, err := NewFlake8().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: err = %v", argv, err)
		}
	}
}
