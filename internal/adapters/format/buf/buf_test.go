package buf

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: buf 1.x on macOS arm64 against a small proto module (lint with
// the STANDARD rules, a missing import, a breaking change against a saved
// image, and a generate with an absent plugin).

func inputOf(t *testing.T, name string, argv ...string) (format.Input, string) {
	t.Helper()
	read := func(k string) string {
		b, err := os.ReadFile("testdata/" + name + "." + k + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	o, e := read("stdout"), read("stderr")
	return format.Input{Argv: argv, ExitCode: 100, Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, o + e
}

func TestGroupsFindingsLosslessly(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"lint-bad", []string{"buf", "lint"}},
		{"breaking", []string{"buf", "breaking", "--against", "x.binpb"}},
		{"build-bad", []string{"buf", "build"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, raw := inputOf(t, tc.name, tc.argv...)
			r, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			// Every raw finding must be reconstructible from the grouped form.
			var file string
			rebuilt := map[string]bool{}
			for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
				if !strings.HasPrefix(l, "  ") {
					file = strings.TrimSuffix(l, ":")
					continue
				}
				pos, msg, _ := strings.Cut(strings.TrimPrefix(l, "  "), " ")
				rebuilt[file+":"+pos+":"+msg] = true
			}
			for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
				if !rebuilt[l] {
					t.Errorf("finding lost: %q\n%s", l, body)
				}
			}
			if r.Elided {
				t.Error("grouping is lossless and must not claim an elision")
			}
		})
	}
}

func TestLintShrinks(t *testing.T) {
	in, raw := inputOf(t, "lint-bad", "buf", "lint")
	r, err := New().Aggressive(context.Background(), in)
	if err != nil || len(r.Body) >= len(raw) {
		t.Fatalf("err=%v render=%d raw=%d", err, len(r.Body), len(raw))
	}
	if !strings.Contains(string(r.Body), "proto/demo/v1/demo.proto:\n  7:9 Message name \"getRequest\" should be PascalCase") {
		t.Errorf("unexpected:\n%s", r.Body)
	}
	if strings.Count(string(r.Body), "demo.proto") != 1 {
		t.Error("path repeated")
	}
}

func TestNonFindingLinesKeptVerbatim(t *testing.T) {
	in, _ := inputOf(t, "gen-noplugin", "buf", "generate")
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("no findings must decline: %v", err)
	}
	in = format.Input{Argv: []string{"buf", "lint"}, Stdout: strings.NewReader("Failure: boom\na.proto:1:1:x\na.proto:2:1:y\n"), Stderr: strings.NewReader("")}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil || !strings.HasPrefix(string(r.Body), "Failure: boom\na.proto:\n  1:1 x\n  2:1 y") {
		t.Errorf("err=%v body=%q", err, r.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"buf", "lint", "--error-format=json"}, {"buf", "lint", "--error-format", "github-actions"},
		{"buf", "format", "-d"}, {"buf", "ls-files"}, {"buf", "lint", "--help"}, {"buf", "--version"}, {"buf", "mod", "update"},
	} {
		in, _ := inputOf(t, "lint-bad", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: err=%v", argv, err)
		}
	}
	in, _ := inputOf(t, "lint-json", "buf", "lint")
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Error("JSON lines must decline")
	}
	in, _ = inputOf(t, "lint-bad", "buf", "lint", "--error-format", "text")
	if _, err := New().Aggressive(context.Background(), in); err != nil {
		t.Errorf("--error-format text must be handled: %v", err)
	}
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Error("relaxed must decline")
	}
}
