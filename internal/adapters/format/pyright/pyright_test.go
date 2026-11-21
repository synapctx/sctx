package pyright

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
	n, _ := strconv.Atoi(strings.TrimSpace(string(code)))
	return string(raw), n
}

func TestRealFixtures(t *testing.T) {
	getwd = func() (string, error) { return "/work/proj", nil }
	t.Cleanup(func() { getwd = os.Getwd })
	tests := []struct {
		name, fixture string
		f             *Formatter
		argv          []string
		keep, drop    []string
	}{
		{
			name: "pyright errors keep every diagnostic with rule and detail", fixture: "pyright", f: New(),
			argv: []string{"pyright", "pkg"},
			keep: []string{
				"pkg/core.py (5)",
				`L12:12 error: Type "int" is not assignable to return type "str"`,
				`"int" is not assignable to "str" (reportReturnType)`,
				`L16:15 error: Cannot access attribute "bit_length" for class "None"`,
				`L19:1 error: "undefined_fn" is not defined (reportUndefinedVariable)`,
				`(reportArgumentType)`, "5 errors, 0 warnings, 0 informations",
			},
			drop: []string{"/work/proj"},
		},
		{
			name: "basedpyright keeps warnings and errors across files", fixture: "basedpyright", f: NewBased(),
			argv: []string{"basedpyright", "pkg"},
			keep: []string{"pkg/core.py (", "pkg/ugly.py (", "L1:8 warning: Import \"os\" is not accessed (reportUnusedImport)",
				"(reportReturnType)", "5 errors, 16 warnings, 0 notes"},
		},
		{
			name: "pyright syntax errors", fixture: "pyright_syntax", f: New(),
			argv: []string{"pyright", "bad.py"},
			keep: []string{"synt/bad.py (7)", `L1:6 error: "(" was not closed`, `L4:1 error: Expected ":"`, "7 errors"},
		},
		{name: "pyright clean", fixture: "pyright_clean", f: New(), argv: []string{"pyright", "m.py"},
			keep: []string{"0 errors, 0 warnings, 0 informations"}},
		{
			name: "ty keeps location rule message and labels", fixture: "ty", f: NewTy(),
			argv: []string{"ty", "check", "pkg"},
			keep: []string{
				"pkg/core.py:12:12 error[invalid-explicit-return]: Return type does not match returned value",
				"expected `str`, found `int`",
				"pkg/core.py:19:1 error[unresolved-reference]: Name `undefined_fn` used when not defined",
				"info: Function defined here (pkg/core.py:5:5)", "Found 5 diagnostics",
			},
			drop: []string{"  |", "def bad"},
		},
		{name: "ty syntax", fixture: "ty_syntax", f: NewTy(), argv: []string{"ty", "check", "bad.py"},
			keep: []string{"bad.py:3:5 error[invalid-syntax]: Expected an expression", "Found 3 diagnostics"}},
		{name: "ty clean", fixture: "ty_clean", f: NewTy(), argv: []string{"ty", "check", "m.py"},
			keep: []string{"All checks passed!"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, exit := fixture(t, tt.fixture)
			in := func() format.Input {
				return format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw), ExitCode: exit}
			}
			for tier, run := range map[string]func(context.Context, format.Input) (format.Rendered, error){
				"aggressive": tt.f.Aggressive, "relaxed": tt.f.Relaxed,
			} {
				out, err := run(context.Background(), in())
				if err != nil {
					t.Fatalf("%s: %v", tier, err)
				}
				body := string(out.Body)
				if tier == "aggressive" {
					t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
				}
				for _, k := range tt.keep {
					if !strings.Contains(body, k) {
						t.Errorf("%s: missing %q", tier, k)
					}
				}
				for _, d := range tt.drop {
					if strings.Contains(body, d) {
						t.Errorf("%s: unexpected %q", tier, d)
					}
				}
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	jsonOut := "{\n  \"version\": \"1.1\",\n  \"generalDiagnostics\": []\n}\n"
	tests := []struct {
		name string
		f    *Formatter
		argv []string
		out  string
	}{
		{"pyright --outputjson", New(), []string{"pyright", "--outputjson", "."}, jsonOut},
		{"pyright json by content", New(), []string{"pyright", "."}, jsonOut},
		{"pyright --verifytypes", New(), []string{"pyright", "--verifytypes", "x"}, "Module name: x\n"},
		{"ty server", NewTy(), []string{"ty", "server"}, "x\n"},
		{"ty concise output", NewTy(), []string{"ty", "check", "--output-format", "concise"}, "a.py:1:1: error[x] y\n"},
		{"not pyright shaped", New(), []string{"pyright", "."}, "something else\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, run := range []func(context.Context, format.Input) (format.Rendered, error){tt.f.Aggressive, tt.f.Relaxed} {
				_, err := run(context.Background(), format.Input{Argv: tt.argv, Stdout: strings.NewReader(tt.out)})
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Errorf("err = %v, want ErrTierInapplicable", err)
				}
			}
		})
	}
}

func TestLongDetailIsCappedWithMarker(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("/work/proj/a.py\n  /work/proj/a.py:3:1 - error: Argument mismatch\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("  \u00a0\u00a0detail line\n")
	}
	sb.WriteString("  \u00a0\u00a0last (reportArgumentType)\n1 error, 0 warnings, 0 informations \n")
	getwd = func() (string, error) { return "/work/proj", nil }
	t.Cleanup(func() { getwd = os.Getwd })
	in := func() format.Input {
		return format.Input{Argv: []string{"pyright"}, Stdout: strings.NewReader(sb.String()), ExitCode: 1}
	}
	out, err := New().Aggressive(context.Background(), in())
	if err != nil || !strings.Contains(string(out.Body), "…+5 lines") || !out.Elided {
		t.Fatalf("err=%v body=%q", err, out.Body)
	}
	rel, _ := New().Relaxed(context.Background(), in())
	if !strings.Contains(string(rel.Body), "last (reportArgumentType)") {
		t.Errorf("relaxed lost the rule line: %q", rel.Body)
	}
}
