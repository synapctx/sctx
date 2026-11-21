package npx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const tscOut = "src/a.ts(1,2): error TS2322: bad\nsrc/a.ts(3,4): error TS2345: worse\n"

const jestOut = "PASS src/a.test.ts\n\nTest Suites: 1 passed, 1 total\nTests:       3 passed, 3 total\nSnapshots:   0 total\nTime:        1.5 s\n"

func in(argv []string, stdout, stderr string, exit int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
}

func TestNpxDispatchesOnNamedTool(t *testing.T) {
	tests := []struct {
		name   string
		argv   []string
		out    string
		stderr string
		want   string
	}{
		{"tsc", []string{"npx", "tsc", "--noEmit"}, tscOut, "", "2 errors in 1 file"},
		{"flags before tool", []string{"npx", "--yes", "tsc"}, tscOut, "", "codes: TS2322=1 TS2345=1"},
		{"jest reports on stderr", []string{"npx", "jest"}, "", jestOut, "jest: 3 passed, 1 suites, 1.5s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New().Aggressive(context.Background(), in(tc.argv, tc.out, tc.stderr, 0))
			if err != nil || !strings.Contains(string(got.Body), tc.want) {
				t.Fatalf("err=%v body=%s", err, got.Body)
			}
		})
	}
}

func TestNpxDeclinesUnknownTool(t *testing.T) {
	_, err := New().Aggressive(context.Background(), in([]string{"npx", "prettier", "."}, tscOut, "", 0))
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("want ErrTierInapplicable, got %v", err)
	}
}

func TestSniffRecognisesByShapeOnly(t *testing.T) {
	if _, err := Sniff(in([]string{"npm", "test"}, "", jestOut, 0)); err != nil {
		t.Fatalf("jest shape: %v", err)
	}
	if _, err := Sniff(in([]string{"npm", "run", "build"}, "building...\ndone\n", "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("unrelated output must decline, got %v", err)
	}
}
