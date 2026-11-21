package eslint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(argv []string, raw []byte, exit int) (format.Rendered, error) {
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
		{"eslint-errors.txt", 1, []string{
			"errors in 12 files",
			"L68:58 curly: Expected { after 'if' condition",
			"L71:17 no-undef: 'console' is not defined",
			"rules: no-undef=",
			"12 errors and 0 warnings potentially fixable",
		}, []string{"warn "}},
		{"eslint-warnings.txt", 0, []string{
			"12 warnings in 4 files",
			"src/AIProviderManager.ts — 2 warnings",
			"L68:58 warn curly: Expected { after 'if' condition",
			"rules: curly=12",
			"…+1 more warnings in file",
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := run([]string{"eslint", "src"}, raw, tc.exit)
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

func TestEveryErrorKept(t *testing.T) {
	raw, _ := os.ReadFile("testdata/eslint-errors.txt")
	got, err := run([]string{"eslint"}, raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got.Body), "\n  L"); n != 115 {
		t.Errorf("kept %d problem lines, want 115", n)
	}
}

func TestDeclines(t *testing.T) {
	stylish := []byte("\n/a.js\n  1:1  error  x  no-x\n\n✖ 1 problem (1 error, 0 warnings)\n")
	tests := []struct {
		name string
		argv []string
		raw  []byte
	}{
		{"json format", []string{"eslint", "-f", "json", "."}, stylish},
		{"format= flag", []string{"eslint", "--format=compact", "."}, stylish},
		{"no problems", []string{"eslint"}, []byte("Oops\n")},
		{"summary disagrees", []string{"eslint"}, []byte("\n/a.js\n  1:1  error  x  no-x\n\n✖ 5 problems (5 errors, 0 warnings)\n")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := run(tc.argv, tc.raw, 1); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

func TestNoRuleParserError(t *testing.T) {
	raw := []byte("\n/a.js\n  3:5  error  Parsing error: Unexpected token )\n\n✖ 1 problem (1 error, 0 warnings)\n")
	got, err := run([]string{"eslint"}, raw, 1)
	if err != nil || !strings.Contains(string(got.Body), "L3:5 Parsing error: Unexpected token )") {
		t.Fatalf("err=%v body=%s", err, got.Body)
	}
}
