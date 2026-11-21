package biome

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func run(t *testing.T, f *Formatter, argv []string, file string, exit int) (format.Rendered, int, error) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Aggressive(context.Background(), format.Input{
		Argv: argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: exit,
	})
	return got, len(raw), err
}

func TestRealSamples(t *testing.T) {
	tests := []struct {
		name   string
		f      *Formatter
		argv   []string
		file   string
		exit   int
		want   []string
		absent []string
	}{
		{"biome check keeps every finding", NewBiome(), []string{"biome", "check", "src"}, "biome-check-fail.txt", 1,
			[]string{
				"biome: 4 errors, 4 warnings in 2 files",
				"src/a.js", "2:43 lint/suspicious/noDoubleEquals: Using == may be unsafe",
				"4:1 lint/suspicious/noDebugger", "1:7 warn lint/correctness/noUnusedVariables",
				"src/b.ts", "format: needs formatting", "Checked 3 files in 12ms", "…+",
			}, []string{"━", "Unsafe fix", "const·x"}},
		{"biome lint", NewBiome(), []string{"biome", "lint", "src"}, "biome-lint.txt", 1,
			[]string{"biome: 2 errors, 4 warnings", "4:1 lint/suspicious/noDebugger"}, []string{"│"}},
		{"biome format", NewBiome(), []string{"biome", "format", "src"}, "biome-format.txt", 1,
			[]string{"src/a.js", "format: needs formatting", "src/b.ts"}, []string{"·"}},
		{"biome ci with colour and hyperlinks", NewBiome(), []string{"biome", "ci", "src"}, "biome-ci-color.txt", 1,
			[]string{"biome: 4 errors, 4 warnings", "4:1 lint/suspicious/noDebugger", "format: File content differs"}, []string{"\x1b", "https://"}},
		{"parse errors dedupe with a count", NewBiome(), []string{"biome", "check", "srcp"}, "biome-parse.txt", 1,
			[]string{"1:7 parse: Expected an identifier", "×2", "1:9 parse", "3:1 parse: expected `}`"}, nil},
		{"oxlint default", NewOxlint(), []string{"oxlint", "src"}, "oxlint-default.txt", 1,
			[]string{"oxlint: 4 warnings in 2 files", "4:1 warn eslint(no-debugger)", "src/b.ts"}, []string{",-[", "`----"}},
		{"oxlint compact", NewOxlint(), []string{"oxlint", "src"}, "oxlint-compact.txt", 0,
			[]string{"oxlint: 4 warnings in 2 files", "4:1 warn eslint(no-debugger)", "…+4 detail lines"}, []string{"help:"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, raw, err := run(t, tc.f, tc.argv, tc.file, tc.exit)
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
			if len(got.Body) >= raw && tc.file != "oxlint-compact.txt" {
				t.Errorf("not smaller: %d >= %d", len(got.Body), raw)
			}
			t.Logf("%s: %d -> %d bytes", tc.file, raw, len(got.Body))
		})
	}
}

func TestDeclines(t *testing.T) {
	tests := []struct {
		name string
		f    *Formatter
		argv []string
		file string
		out  string
	}{
		{"clean run has nothing to render", NewBiome(), []string{"biome", "check"}, "biome-check-pass.txt", ""},
		{"json reporter", NewBiome(), []string{"biome", "check", "--reporter=json"}, "biome-check-fail.txt", ""},
		{"github reporter spaced", NewBiome(), []string{"biome", "ci", "--reporter", "github"}, "biome-check-fail.txt", ""},
		{"oxlint json", NewOxlint(), []string{"oxlint", "--format=json"}, "oxlint-default.txt", ""},
		{"unknown text", NewBiome(), []string{"biome", "check"}, "", "hello\nworld\n"},
		{"oxlint count mismatch", NewOxlint(), []string{"oxlint"}, "", "  ! eslint(no-debugger): x\n   ,-[a.js:1:1]\n 1 | d\n   `----\nFound 3 warnings and 0 errors.\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.out)
			if tc.file != "" {
				var err error
				if raw, err = os.ReadFile("testdata/" + tc.file); err != nil {
					t.Fatal(err)
				}
			}
			_, err := tc.f.Aggressive(context.Background(), format.Input{
				Argv: tc.argv, Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(raw), ExitCode: 1,
			})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

// A non-zero exit must keep every finding's location, rule and message.
func TestNonZeroExitKeepsEveryError(t *testing.T) {
	got, _, err := run(t, NewBiome(), []string{"biome", "check", "srcp"}, "biome-parse.txt", 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("testdata/biome-parse.txt")
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, " parse ━") {
			pos := strings.Fields(line)[0]
			pos = pos[strings.Index(pos, ":")+1:]
			if !strings.Contains(string(got.Body), pos+" parse") {
				t.Errorf("lost parse error at %s:\n%s", pos, got.Body)
			}
		}
	}
	if !got.Elided {
		t.Error("detail lines were dropped but Elided is false")
	}
}

func TestRelaxedCollapsesRuns(t *testing.T) {
	f := NewBiome()
	_, err := f.Relaxed(context.Background(), format.Input{Stdout: strings.NewReader("a\nb\n"), Stderr: strings.NewReader("")})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("want decline, got %v", err)
	}
	got, err := f.Relaxed(context.Background(), format.Input{Stdout: strings.NewReader(strings.Repeat("same\n", 5)), Stderr: strings.NewReader("")})
	if err != nil || !strings.Contains(string(got.Body), "×5") {
		t.Fatalf("got %q err %v", got.Body, err)
	}
}
