package nix

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

// Fixtures were captured from elixir:1.17 (nixos/nix:2.24.10 (Nix 2.24.10).
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

func forCmd(c string) *Formatter { return &Formatter{command: c} }

func TestRendersKeepErrorSignal(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		argv []string
		keep []string
		drop []string
	}{
		{"nx-shell", "nix-shell", []string{"nix-shell", "-p", "hello", "--run", "hello"},
			[]string{"these 25 paths will be fetched (52.95 MiB download, 261.81 MiB unpacked):", "Hello, world!", "…+25 store paths listed", "…+25 copying-path rows"},
			[]string{"/nix/store/v8ax58gdkdg32141az8mg9gxcq9jgmkr-binutils-2.41", "copying path"}},
		{"nx-build-hello", "nix", []string{"nix", "build", "nixpkgs#cowsay", "--no-link"},
			[]string{"these 16 paths will be fetched (28.36 MiB download, 120.67 MiB unpacked):", "…+16 store paths listed", "…+16 copying-path rows"},
			[]string{"cowsay-3.8.4-man"}},
		{"nx-build-fail", "nix-build", []string{"nix-build", "fail.nix", "--no-out-link"},
			[]string{"building '/nix/store/zs7mq1c83gfbr54xd8g6czibqdbr4l31-failing.drv'...", "compiling stuff", "error: widget missing",
				"error: builder for '/nix/store/zs7mq1c83gfbr54xd8g6czibqdbr4l31-failing.drv' failed with exit code 2", "this derivation will be built:"},
			[]string{"\n  /nix/store/"}},
		{"nx-flake-check-bad", "nix", []string{"nix", "flake", "check", "path:."},
			[]string{"error: this output is broken", "… while checking the derivation 'packages.aarch64-linux.default'", "packages.aarch64-linux.default = throw \"this output is broken\";", "(stack trace truncated"},
			[]string{"checking derivation packages.aarch64-linux.default...", "evaluating flake..."}},
		{"nx-flake-check", "nix", []string{"nix", "flake", "check", "path:."},
			[]string{"running 1 flake checks...", "building '/nix/store/y1sxyd4pgfli8ilmbpa7fgrjwnazby4d-t1.drv'..."},
			[]string{"checking derivation packages.aarch64-linux.other...", "derivation evaluated to"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, code := load(t, tc.name)
			in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
			out, err := forCmd(tc.cmd).Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("render %d >= raw %d", len(body), len(raw))
			}
			if !strings.Contains(body, "…+") {
				t.Error("elision not marked")
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
			// Every error line of the raw output survives.
			for _, l := range strings.Split(raw, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "error:") && !strings.Contains(body, strings.TrimRight(l, " ")) {
					t.Errorf("error line lost: %q", l)
				}
			}
			golden(t, tc.name, out.Body)

			rel, err := forCmd(tc.cmd).Relaxed(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code})
			if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatal(err)
			}
			if err == nil && !strings.Contains(string(rel.Body), "…+") {
				t.Error("relaxed elision not marked")
			}
		})
	}
}

func TestSmallOutputsDecline(t *testing.T) {
	for _, n := range []string{"nx-eval-err"} {
		raw, code := load(t, n)
		in := format.Input{Argv: []string{"nix-build", "x.nix"}, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := forCmd("nix-build").Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%s: got %v", n, err)
		}
	}
}

func TestDeclines(t *testing.T) {
	raw, code := load(t, "nx-shell")
	for _, tc := range []struct {
		cmd  string
		argv []string
	}{
		{"nix", []string{"nix"}}, {"nix", []string{"nix", "run", "nixpkgs#hello"}}, {"nix", []string{"nix", "develop"}}, {"nix", []string{"nix", "shell", "nixpkgs#hello"}},
		{"nix", []string{"nix", "repl"}}, {"nix", []string{"nix", "eval", "--json", "x"}}, {"nix", []string{"nix", "build", "--json", "x"}}, {"nix", []string{"nix", "build", "--log-format", "internal-json", "x"}},
		{"nix", []string{"nix", "flake", "update"}}, {"nix", []string{"nix", "store", "gc"}},
		{"nix-shell", []string{"nix-shell", "-p", "hello"}}, {"nix-shell", []string{"nix-shell"}},
	} {
		in := format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw), ExitCode: code}
		if _, err := forCmd(tc.cmd).Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: got %v", tc.argv, err)
		}
	}
}

func TestAllCoversPrograms(t *testing.T) {
	got := map[string]bool{}
	for _, f := range All() {
		got[f.Descriptor().Command] = true
	}
	for _, n := range []string{"nix", "nix-build", "nix-shell"} {
		if !got[n] {
			t.Errorf("missing %s", n)
		}
	}
}
