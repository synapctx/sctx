package playwright

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func render(t *testing.T, argv []string, raw []byte, exit int) (format.Rendered, error) {
	t.Helper()
	return New().Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(raw), Stderr: bytes.NewReader(nil), ExitCode: exit,
	})
}

func TestRealSamples(t *testing.T) {
	tests := []struct {
		file   string
		exit   int
		want   []string
		absent []string
	}{
		{"pw-pass.txt", 0, []string{"playwright: 3 passed, 359ms"}, []string{"✓", "…+"}},
		{"pw-fail.txt", 1, []string{
			"playwright: 3 failed, 1 skipped, 7 passed, 1.2s",
			"FAIL tests/api.spec.js:4:2 › math › adds wrong",
			"Expected: 4", "Received: 3", "at tests/api.spec.js:4:37",
			"-     3,", "+     2,",
			"FAIL tests/b.spec.js:4:1 › timeout-ish",
			"files: test-results/b-timeout-ish/error-context.md",
		}, []string{"> 4 |", "/private/", "✘"}},
		{"pw-ui-fail.txt", 1, []string{
			"Locator: getByRole('button', { name: 'Submit' })",
			"Error: element(s) not found",
			`Expected: "Goodbye"`, `Received: "Hello"`,
			"TimeoutError: page.click: Timeout 500ms exceeded.",
			"- waiting for locator('#nope')",
			"test-results/ui-ui-missing/{test-failed-1.png,error-context.md,trace.zip}",
		}, []string{"show-trace", "Usage:", "attachment #"}},
		{"pw-ui-retry.txt", 1, []string{"ui missing (2 attempts)", "ui click timeout (2 attempts)"}, []string{"Retry #1", "retry1"}},
		{"pw-ui-dot.txt", 1, []string{"playwright: 3 failed, 1 passed, 4.1s", "FAIL tests/ui.spec.js:5:1 › ui click timeout"}, nil},
		{"pw-ui-line.txt", 1, []string{"FAIL tests/ui.spec.js:4:1 › ui text mismatch"}, []string{"[3/4]", "[4/4]"}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := render(t, []string{"playwright", "test"}, raw, tc.exit)
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
			if len(got.Body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(got.Body), len(raw))
			}
			if strings.Contains(body, "…+") != got.Elided {
				t.Errorf("Elided=%v disagrees with markers in:\n%s", got.Elided, body)
			}
			t.Logf("%s: %d -> %d", tc.file, len(raw), len(got.Body))
		})
	}
}

// Every failing test in the raw output must survive on a non-zero exit.
func TestEveryFailingTestSurvives(t *testing.T) {
	raw, _ := os.ReadFile("testdata/pw-ui-fail.txt")
	got, err := render(t, []string{"playwright", "test"}, raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"ui missing", "ui text mismatch", "ui click timeout"} {
		if !strings.Contains(string(got.Body), "FAIL tests/ui.spec.js:") || !strings.Contains(string(got.Body), "› "+title) {
			t.Errorf("lost failing test %q:\n%s", title, got.Body)
		}
	}
}

func TestDeclines(t *testing.T) {
	passRaw, _ := os.ReadFile("testdata/pw-fail.txt")
	tests := []struct {
		name string
		argv []string
		out  string
		exit int
	}{
		{"not playwright output", []string{"playwright", "test"}, "hello\n", 0},
		{"html reporter", []string{"playwright", "test", "--reporter=html"}, string(passRaw), 1},
		{"json reporter spaced", []string{"playwright", "test", "--reporter", "json"}, string(passRaw), 1},
		{"non-zero exit with nothing attributable", []string{"playwright", "test"}, "  1 interrupted\n  2 passed (1s)\n", 1},
		{"failed count without blocks", []string{"playwright", "test"}, "  2 failed\n  1 passed (1s)\n", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := render(t, tc.argv, []byte(tc.out), tc.exit)
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

func TestDescriptorOnlyClaimsTest(t *testing.T) {
	d := New().Descriptor()
	if d.Command != "playwright" || len(d.Subcommands) != 1 || d.Subcommands[0] != "test" {
		t.Fatalf("descriptor %+v", d)
	}
}
