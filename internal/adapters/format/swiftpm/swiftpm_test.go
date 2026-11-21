package swiftpm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		argv []string
		exit int
		keep []string
		drop []string
	}{
		{"build-ok.txt", []string{"swift", "build"}, 0,
			[]string{"Demo.swift:2:32: warning: initialization of immutable value 'x' was never used", "Build complete! (26.96 secs)", "ld: warning: search path", "…+"},
			[]string{"[19 / 28]", "public func unusedWarn", "[#NoUsage]: <", "\x1b"}},
		{"build-err.txt", []string{"swift", "build"}, 1,
			[]string{"Bad.swift:2:21: error: cannot convert value of type 'Int' to specified type 'String'", "Bad.swift:3:12: error: cannot find 'undefinedThing' in scope", "Bad.swift:5:23: error: cannot find 'missingFn' in scope", "error: Build failed", "builtin-SwiftPerFileCompile Bad.swift", "Failed frontend command: …+"},
			[]string{"let s: String = 5", "swift-frontend -frontend -c -primary-file"}},
		{"test-fail.txt", []string{"swift", "test"}, 1,
			[]string{"error: external macro implementation type 'TestingMacros.TestDeclarationMacro' could not be found for macro 'Test'", "Testing.Test:1:30: note: 'Test' declared here", "error: Build failed", "error: fatalError"},
			[]string{"@attached(peer) public macro Test"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			for name, tier := range map[string]func(*Formatter, context.Context, format.Input) (format.Rendered, error){"aggressive": (*Formatter).Aggressive, "relaxed": (*Formatter).Relaxed} {
				out, err := tier(New(), context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: tc.exit})
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				body := string(out.Body)
				if len(body) >= len(raw) {
					t.Errorf("%s: not smaller %d >= %d", name, len(body), len(raw))
				}
				if strings.Contains(body, "\x1b") {
					t.Errorf("%s: ANSI left in", name)
				}
				for _, l := range strings.Split(body, "\n") {
					if progressRE.MatchString(l) {
						t.Errorf("%s: progress line left: %q", name, l)
					}
				}
				if name == "relaxed" {
					continue
				}
				for _, k := range tc.keep {
					if !strings.Contains(body, k) {
						t.Errorf("lost %q:\n%s", k, body)
					}
				}
				for _, d := range tc.drop {
					if strings.Contains(body, d) {
						t.Errorf("kept %q", d)
					}
				}
			}
		})
	}
}

func TestNoopBuildDeclines(t *testing.T) {
	_, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"swift", "build"}, Stdout: strings.NewReader(fixture(t, "build-noop.txt"))})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("got %v", err)
	}
}

func TestOtherSubcommandsAndPayloadFlagsDecline(t *testing.T) {
	raw := fixture(t, "build-ok.txt")
	for _, argv := range [][]string{{"swift", "run"}, {"swift", "package", "resolve"}, {"swift", "build", "--show-bin-path"}, {"swift", "test", "--list-tests"}, {"swift", "build", "-v"}, {"swift", "--version"}} {
		if _, err := New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw)}); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}
