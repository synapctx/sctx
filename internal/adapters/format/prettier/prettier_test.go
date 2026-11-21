package prettier

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(t *testing.T, argv []string, file string, exit int) (format.Rendered, int, error) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New().Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: exit,
	})
	return got, len(raw), err
}

type sample struct {
	file   string
	exit   int
	want   []string
	absent []string
}

func check(t *testing.T, argv []string, tests []sample, mustShrink bool) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got, raw, err := run(t, argv, tc.file, tc.exit)
			if err != nil {
				t.Fatal(err)
			}
			body := string(got.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Errorf("unexpected %q in:\n%s", a, body)
				}
			}
			if mustShrink && len(got.Body) >= raw {
				t.Errorf("not smaller: %d >= %d", len(got.Body), raw)
			}
			if strings.Contains(body, "…+") != got.Elided {
				t.Errorf("Elided=%v disagrees with markers in:\n%s", got.Elided, body)
			}
		})
	}
}

func declines(t *testing.T, argv []string, out string, exit int) {
	t.Helper()
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: strings.NewReader(out), Stderr: strings.NewReader(""), ExitCode: exit,
	})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("want ErrTierInapplicable, got %v", err)
	}
}

func TestRealSamples(t *testing.T) {
	check(t, []string{"prettier", "--check", "src"}, []sample{
		{"check-fail.txt", 2, []string{"prettier: 2 files need formatting", "src/a.js", "src/b.ts"}, []string{"[warn]", "Checking"}},
		{"check-pass.txt", 0, []string{"prettier: all matched files formatted"}, nil},
		{"check-syntax.txt", 2, []string{"1 file needs formatting, errors", "src3/a.js", "error: src3/syn.js: SyntaxError: Unexpected token (2:8)", "Error occurred when checking", "…+4 code-frame lines"}, []string{"[error]", "b: 1,,"}},
		{"write-many.txt", 0, []string{"prettier: 6 written, 1 unchanged", "src4/f6.js"}, []string{"ms"}},
		{"write-cache.txt", 0, []string{"prettier: 0 written, 7 unchanged"}, []string{"cached"}},
		{"write-syntax.txt", 2, []string{"1 written, 1 unchanged", "error: src3/syn.js: SyntaxError"}, nil},
	}, true)
}

func TestDeclines(t *testing.T) {
	declines(t, []string{"prettier", "-l", "src"}, "src/a.js\nsrc/b.ts\n", 1)
	declines(t, []string{"prettier", "--check"}, "hello\n", 0)
	declines(t, []string{"prettier", "--help"}, "Usage: prettier\n", 0)
}

// A non-zero exit must keep every file that needs formatting.
func TestNonZeroExitKeepsEveryFile(t *testing.T) {
	got, _, err := run(t, []string{"prettier", "--check"}, "check-fail.txt", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/a.js", "src/b.ts"} {
		if !strings.Contains(string(got.Body), f) {
			t.Errorf("lost %s: %s", f, got.Body)
		}
	}
}
