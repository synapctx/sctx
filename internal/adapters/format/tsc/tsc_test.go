package tsc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(t *testing.T, raw []byte, exit int) (format.Rendered, error) {
	t.Helper()
	return New().Aggressive(context.Background(), format.Input{
		Argv: []string{"tsc"}, Stdout: bytes.NewReader(raw), Stderr: bytes.NewReader(nil), ExitCode: exit,
	})
}

func TestRealSamples(t *testing.T) {
	tests := []struct {
		file   string
		want   []string
		absent []string
	}{
		{"tsc-classic-src.txt", []string{
			"src/AIProviderManager.ts — 2 errors",
			"L33:17 TS6138: Property 'openaiApiKey' is declared but its value is never read.",
			"26 errors in 7 files",
			"codes: TS6133=24 TS6138=2",
		}, []string{"…+"}},
		{"tsc-pretty-src.txt", []string{
			"src/AIProviderManager.ts — 2 errors",
			"L293:65 TS6133: 'key' is declared but its value is never read.",
			"26 errors in 7 files",
			"codes: TS6133=24 TS6138=2",
			"code-frame lines",
		}, []string{"~~~", "Errors  Files", "Found 26"}},
		{"tsc-classic-lib.txt", []string{"node_modules/ulid/dist/index.d.cts — 6 errors", "6 errors in 1 file", "codes: TS1479=6"}, nil},
		{"tsc-pretty-lib.txt", []string{"6 errors in 1 file", "codes: TS1479=6"}, []string{"~~~"}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := run(t, raw, 2)
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
			t.Logf("%s: %d -> %d bytes", tc.file, len(raw), len(got.Body))
		})
	}
}

func TestContinuationAndGlobal(t *testing.T) {
	raw := "src/a.ts(1,2): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"  Types of property 'x' are incompatible.\n" +
		"error TS5083: Cannot read file 'tsconfig.json'.\n"
	got, err := run(t, []byte(raw), 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"    Types of property 'x' are incompatible.", "(global) — 1 error", "TS5083: Cannot read file"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q in:\n%s", w, got.Body)
		}
	}
}

func TestPerFileCapOnlyWhenManyFiles(t *testing.T) {
	var many, few strings.Builder
	for f := 0; f < 25; f++ {
		for i := 0; i < 12; i++ {
			fmt.Fprintf(&many, "src/f%d.ts(%d,1): error TS2322: bad %d\n", f, i+1, i)
		}
	}
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&few, "src/only.ts(%d,1): error TS2322: bad %d\n", i+1, i)
	}
	got, _ := run(t, []byte(many.String()), 2)
	if !strings.Contains(string(got.Body), "…+2 more in file") || !got.Elided || !strings.Contains(string(got.Body), "300 errors in 25 files") {
		t.Errorf("many files: cap marker/summary missing:\n%s", got.Body)
	}
	got, _ = run(t, []byte(few.String()), 2)
	if strings.Contains(string(got.Body), "more in file") || strings.Count(string(got.Body), "TS2322:") != 12 {
		t.Errorf("few files must keep every error:\n%s", got.Body)
	}
}

func TestDeclines(t *testing.T) {
	for name, raw := range map[string]string{
		"no diagnostics":        "hello\n",
		"found count disagrees": "src/a.ts(1,1): error TS1: x\n\nFound 3 errors in 1 file.\n",
	} {
		if _, err := run(t, []byte(raw), 2); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%s: want ErrTierInapplicable, got %v", name, err)
		}
	}
}
