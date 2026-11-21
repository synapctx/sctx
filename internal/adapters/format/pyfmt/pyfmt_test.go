package pyfmt

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
		{"black --check lists every file", "black_check", New(), []string{"black", "--check", "."},
			[]string{"would reformat:", "  pkg/core.py", "  pkg/ugly.py", "  tests/test_broken.py", "  tests/test_core.py",
				"  tests/test_unit.py", "5 files would be reformatted, 1 file would be left unchanged."},
			[]string{"Oh no", "/work/proj"}},
		{"black clean", "black_clean", New(), []string{"black", "--check", "."},
			[]string{"1 file would be left unchanged."}, []string{"All done"}},
		{"black parse error keeps the error and caret", "black_syntax", New(), []string{"black", "--check", "bad.py"},
			[]string{"error: cannot parse: bad.py:1:6", "def f(:", "ParseError: bad input", "1 file would fail to reformat."},
			[]string{"Oh no"}},
		{"black applied", "black_apply", New(), []string{"black", "."},
			[]string{"7 files left unchanged."}, nil},
		{"isort --check-only", "isort_check", NewIsort(), []string{"isort", "--check-only", "."},
			[]string{"imports unsorted (5):", "  tests/test_broken.py", "  pkg/ugly.py", "Skipped 1 files"},
			[]string{"Imports are incorrectly", "/work/proj"}},
		{"isort clean", "isort_clean", NewIsort(), []string{"isort", "--check-only", "."}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, exit := fixture(t, tt.fixture)
			// The tools write to stderr; feed it there like the real thing.
			in := format.Input{Argv: tt.argv, Stderr: strings.NewReader(raw), Stdout: strings.NewReader(""), ExitCode: exit}
			out, err := tt.f.Aggressive(context.Background(), in)
			if tt.keep == nil {
				if err == nil {
					t.Fatalf("expected decline on empty-signal output, got %q", out.Body)
				}
				return
			}
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
			}
			body := string(out.Body)
			t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
			if !out.FoldStderr {
				t.Error("stderr was consumed but FoldStderr is false")
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

func TestDiffIsTheUsersData(t *testing.T) {
	for _, f := range All() {
		for _, flag := range []string{"--diff", "-d"} {
			in := format.Input{Argv: []string{f.command, flag, "."}, Stdout: strings.NewReader("--- a\n+++ b\n")}
			if _, err := f.Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("%s %s: err = %v", f.command, flag, err)
			}
		}
	}
}

func TestFileListCapIsAnnounced(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxFiles+7; i++ {
		sb.WriteString("would reformat /work/proj/f" + strconv.Itoa(i) + ".py\n")
	}
	sb.WriteString("\nOh no! x\n47 files would be reformatted.\n")
	in := format.Input{Argv: []string{"black", "--check", "."}, Stderr: strings.NewReader(sb.String())}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil || !strings.Contains(string(out.Body), "…+7 more") || !strings.Contains(string(out.Body), "47 files would be reformatted.") {
		t.Fatalf("err=%v body=%q", err, out.Body)
	}
}
