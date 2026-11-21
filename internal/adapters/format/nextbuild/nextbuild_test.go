package nextbuild

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
	check(t, []string{"next", "build"}, []sample{
		{"build-pass.txt", 0, []string{"▲ Next.js 16.4.6 (Turbopack)", "✓ Compiled successfully in 1414ms", "Route (app)", "├ ƒ /blog/[slug]", "└ ○ /lint"}, []string{"Creating an optimized", "(Static)", "telemetry", "Running next.config", "(2/6)"}},
		{"build-webpack.txt", 0, []string{"(webpack)", "Route (app)", "✓ Generating static pages using 8 workers (6/6)"}, []string{"Collecting build traces"}},
		{"build-typeerr.txt", 1, []string{"app/bad/page.tsx(1,7): error TS2322", "app/bad/page.tsx(2,7): error TS2322", "Failed to type check."}, nil},
		{"build-moderr.txt", 1, []string{"./fx-next/app/bad/page.tsx:1:1", "./fx-next/app/bad/page.tsx:2:1", "Module not found: Can't resolve './missing-module'", "Module not found: Can't resolve './missing-two'", "nextjs.org/docs/messages/module-not-found", "…+"}, []string{"\x1b", "import x from"}},
		{"build-syntax.txt", 1, []string{"1:41", "Expression expected", "1:42", "Unterminated regexp literal", "Build error occurred"}, []string{"export default function"}},
	}, true)
}

func TestDeclines(t *testing.T) {
	declines(t, []string{"next", "build"}, "hello\nworld\n", 0)
}

func TestDescriptorOnlyClaimsBuild(t *testing.T) {
	d := New().Descriptor()
	if d.Command != "next" || len(d.Subcommands) != 1 || d.Subcommands[0] != "build" {
		t.Fatalf("descriptor %+v", d)
	}
}
