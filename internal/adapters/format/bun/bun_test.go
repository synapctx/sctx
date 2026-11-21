package bun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(t *testing.T, argv []string, raw []byte, exit int) (format.Rendered, error) {
	t.Helper()
	return New().Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: exit,
	})
}

func TestRealSamples(t *testing.T) {
	tests := []struct {
		file   string
		exit   int
		want   []string
		absent []string
	}{
		{"test-pass.txt", 0, []string{"bun test: 2 pass, 0 fail (2 tests, 1 file, 14.00ms)"}, []string{"…+"}},
		{"test-fail.txt", 1, []string{
			"bun test: 3 pass, 1 skip, 2 fail",
			"FAIL math > adds wrong [0.45ms] — a.test.ts:4:36",
			"Expected: 4", "Received: 3",
			"FAIL math > obj [0.49ms] — a.test.ts:5:39",
			"-     3,", "+     2,", "…+11 code-frame/stack lines",
		}, []string{"1 | import", "/repo/", "^"}},
		{"test-verbose.txt", 1, []string{"FAIL math > obj"}, nil},
		{"test-throw.txt", 1, []string{
			"FAIL throws [0.18ms] — throw.test.ts:2:49", "TypeError: kaboom",
			"FAIL async rej", "error: rejected",
			"FAIL timeout [51.05ms]", "^ this test timed out after 50ms.",
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := run(t, []string{"bun", "test"}, raw, tc.exit)
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
				t.Errorf("Elided=%v disagrees with markers", got.Elided)
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	nomatch, _ := os.ReadFile("testdata/test-nomatch.txt")
	failRaw, _ := os.ReadFile("testdata/test-fail.txt")
	tests := []struct {
		name string
		argv []string
		out  string
		exit int
	}{
		{"no tests matched", []string{"bun", "test", "nonexist"}, string(nomatch), 1},
		{"junit reporter", []string{"bun", "test", "--reporter=junit"}, string(failRaw), 1},
		{"watch mode", []string{"bun", "test", "--watch"}, string(failRaw), 1},
		{"unhandled error between tests", []string{"bun", "test"}, "bun test v1.0.0 (x)\n# Unhandled error between tests\nerror: boom\n 0 pass\n 0 fail\nRan 0 tests across 1 file. [1.00ms]\n", 1},
		{"non-zero exit with no attributable failure", []string{"bun", "test"}, "bun test v1.0.0 (x)\n 1 pass\n 0 fail\nRan 1 tests across 1 file. [1.00ms]\n", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, tc.argv, []byte(tc.out), tc.exit)
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

func TestDescriptorOnlyClaimsTest(t *testing.T) {
	d := New().Descriptor()
	if d.Command != "bun" || len(d.Subcommands) != 1 || d.Subcommands[0] != "test" {
		t.Fatalf("descriptor %+v", d)
	}
}
