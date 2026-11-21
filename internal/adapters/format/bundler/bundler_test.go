package bundler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// load reads a fixture captured from a real Bundler 4 run.
func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile(filepath.Join("testdata", name+".exit"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(e)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

func input(argv []string, out string, code int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(out), ExitCode: code}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s mismatch\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

func TestAggressiveGoldens(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		must []string
	}{
		{"fresh", []string{"bundle", "install"}, []string{"installed 25: rake 13.4.2", "prism 2.1.0 (arm64-darwin)", "json 3.1.0 with native extensions", "Bundle complete! 5 Gemfile dependencies, 25 gems now installed."}},
		{"add", []string{"bundle", "install"}, []string{"installed 1: rack 3.2.9", "Bundle complete! 6 Gemfile dependencies, 26 gems now installed."}},
		{"using", []string{"bundle", "install"}, []string{"Bundle complete! 6 Gemfile"}},
		{"update", []string{"bundle", "update"}, []string{"[DEPRECATED] Pass --all to `bundle update` to update everything", "Bundle updated!"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, code := load(t, tt.name)
			r, err := New().Aggressive(context.Background(), input(tt.argv, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Body) >= len(raw) {
				t.Errorf("render %d bytes is not smaller than raw %d", len(r.Body), len(raw))
			}
			for _, m := range tt.must {
				if !strings.Contains(string(r.Body), m) {
					t.Errorf("lost %q", m)
				}
			}
			golden(t, tt.name, r.Body)
		})
	}
}

// TestFailuresDeclineToRelaxedWithoutLoss: real failure output has no gem
// operations, so the aggressive tier declines and the relaxed tier keeps every
// diagnostic line.
func TestFailuresKeepEveryLine(t *testing.T) {
	for _, name := range []string{"fail", "conflict"} {
		t.Run(name, func(t *testing.T) {
			raw, code := load(t, name)
			if _, err := New().Aggressive(context.Background(), input([]string{"bundle", "install"}, raw, code)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("aggressive err = %v, want decline", err)
			}
			r, err := New().Relaxed(context.Background(), input([]string{"bundle", "install"}, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
				if strings.TrimSpace(l) != "" && !strings.Contains(string(r.Body), strings.TrimRight(l, " ")) {
					t.Errorf("relaxed lost %q", l)
				}
			}
		})
	}
}

func TestFailureAfterPartialInstallKeepsError(t *testing.T) {
	raw := "Fetching gem metadata from https://rubygems.org/.\nResolving dependencies...\nFetching a 1.0\nInstalling a 1.0\nFetching nokogiri 1.16.0\nInstalling nokogiri 1.16.0 with native extensions\n" +
		"Gem::Ext::BuildError: ERROR: Failed to build gem native extension.\n\n    current directory: /x\nmake failed, exit code 2\n\nAn error occurred while installing nokogiri (1.16.0), and Bundler cannot continue.\n"
	r, err := New().Aggressive(context.Background(), input([]string{"bundle", "install"}, raw, 5))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"installed 2: a 1.0, nokogiri 1.16.0 with native extensions", "Gem::Ext::BuildError: ERROR: Failed to build gem native extension.", "    current directory: /x", "make failed, exit code 2", "Bundler cannot continue."} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("lost %q in %q", m, r.Body)
		}
	}
}

func TestFetchedButNotInstalledAndUsingMarkers(t *testing.T) {
	raw := "Using rake 13.0.0\nUsing rack 3.0.0\nFetching x 2.0\nBundle complete! 3 Gemfile dependencies, 3 gems now installed.\n"
	r, err := New().Aggressive(context.Background(), input([]string{"bundle", "install"}, raw, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"using ×2 gems already installed", "fetched but not installed 1: x 2.0"} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("missing %q in %q", m, r.Body)
		}
	}
}

func TestOtherSubcommandsDecline(t *testing.T) {
	for _, argv := range [][]string{{"bundle", "outdated"}, {"bundle", "list"}, {"bundle"}} {
		if _, err := New().Aggressive(context.Background(), input(argv, "rake 13\n", 0)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v err = %v", argv, err)
		}
	}
}

type fakeInner struct{ seen []string }

func (f *fakeInner) Descriptor() format.Match { return format.Match{Command: "rspec"} }
func (f *fakeInner) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	f.seen = in.Argv
	return format.Rendered{Body: []byte("inner")}, nil
}
func (f *fakeInner) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.Aggressive(ctx, in)
}

func TestExecDelegatesToInnerTool(t *testing.T) {
	inner := &fakeInner{}
	f := New(func(argv []string) (format.Formatter, bool) {
		return inner, argv[0] == "rspec"
	})
	r, err := f.Aggressive(context.Background(), input([]string{"bundle", "exec", "--", "rspec", "spec"}, "x\n", 1))
	if err != nil || string(r.Body) != "inner" || strings.Join(inner.seen, " ") != "rspec spec" {
		t.Fatalf("delegation: %v %q %v", err, r.Body, inner.seen)
	}
	if _, err := f.Relaxed(context.Background(), input([]string{"bundle", "exec", "unknown"}, "x\n", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("unresolved exec err = %v", err)
	}
	if _, err := New().Aggressive(context.Background(), input([]string{"bundle", "exec", "rspec"}, "x\n", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("exec without resolver err = %v", err)
	}
}

func TestVerboseDeclines(t *testing.T) {
	raw := "Fetching rack 3.0\nInstalling rack 3.0\nBundle complete! 1 Gemfile dependency, 1 gem now installed.\n"
	if _, err := New().Aggressive(context.Background(), input([]string{"bundle", "install", "--verbose"}, raw, 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("err = %v", err)
	}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "bundle" {
		t.Fatal("wrong command")
	}
}
