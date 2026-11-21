package composer

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

// load reads a fixture captured from a real Composer 2.11 run (stdout and
// stderr merged, as a terminal shows them).
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

// stderrInput feeds the fixture on stderr, where Composer really writes it.
func stderrInput(argv []string, out string, code int) format.Input {
	return format.Input{Argv: argv, Stderr: strings.NewReader(out), Stdout: strings.NewReader(""), ExitCode: code}
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
		{"install-fresh", []string{"composer", "install"}, []string{
			"Installing 2: psr/log 3.0.2, monolog/monolog 3.13.0", "Package operations: 2 installs, 0 updates, 0 removals",
			"Lock file operations: 2 installs, 0 updates, 0 removals", "No composer.lock file present."}},
		{"install-again", []string{"composer", "install"}, []string{"Nothing to install, update or remove"}},
		{"require", []string{"composer", "require", "--dev", "x"}, []string{"Installing 28: laravel/pint v1.32.3", "staabm/side-effects-detector 1.0.5", "Running composer update phpunit/phpunit phpstan/phpstan laravel/pint"}},
		{"update", []string{"composer", "update"}, []string{"Nothing to modify in lock file", "Nothing to install, update or remove"}},
		{"update-nothing", []string{"composer", "update"}, []string{"Nothing to install, update or remove"}},
		{"remove", []string{"composer", "remove", "phpstan/phpstan"}, []string{"Removing 1: phpstan/phpstan 2.3.7", "Lock file operations: 0 installs, 0 updates, 1 removal"}},
		{"require-fail", []string{"composer", "require", "nonexistent-vendor/nope-xyz"}, []string{
			"In PackageDiscoveryTrait.php line 421:", "Could not find a matching version of package nonexistent-vendor/nope-xyz."}},
		{"conflict", []string{"composer", "require", "psr/log:^1.0"}, []string{
			"Your requirements could not be resolved to an installable set of packages.", "Problem 1", "Problem 2",
			"monolog/monolog 3.13.0 requires psr/log ^2.0 || ^3.0", "Use the option --with-all-dependencies (-W)",
			"Installation failed, reverting ./composer.json and ./composer.lock to their original content."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, code := load(t, tt.name)
			r, err := New().Aggressive(context.Background(), stderrInput(tt.argv, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Body) >= len(raw) {
				t.Errorf("render %d bytes is not smaller than raw %d", len(r.Body), len(raw))
			}
			if !r.FoldStderr {
				t.Error("stderr was read but not folded")
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

func TestUpgradeAndDowngradeKeepVersions(t *testing.T) {
	raw := "Loading composer repositories with package information\nUpdating dependencies\nLock file operations: 0 installs, 2 updates, 0 removals\n" +
		"  - Upgrading a/b (1.0.0 => 1.1.0)\n  - Downgrading c/d (3.13.0 => 1.2.1)\nPackage operations: 0 installs, 2 updates, 0 removals\n" +
		"  - Downloading a/b (1.1.0)\n  - Upgrading a/b (1.0.0 => 1.1.0): Extracting archive\n  - Downgrading c/d (3.13.0 => 1.2.1): Extracting archive\n 0/2 [>---]   0%\n 2/2 [====] 100%\n"
	r, err := New().Aggressive(context.Background(), stderrInput([]string{"composer", "update"}, raw, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"Upgrading 1: a/b 1.0.0=>1.1.0", "Downgrading 1: c/d 3.13.0=>1.2.1"} {
		if !strings.Contains(string(r.Body), m) {
			t.Errorf("missing %q in %q", m, r.Body)
		}
	}
}

func TestStdoutIsAlsoRead(t *testing.T) {
	in := format.Input{
		Argv:   []string{"composer", "install"},
		Stdout: strings.NewReader("Nothing to install, update or remove\n"),
		Stderr: strings.NewReader("Loading composer repositories with package information\nWarning: something odd\n"),
	}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "Warning: something odd") || !strings.Contains(string(r.Body), "Nothing to install") {
		t.Errorf("body = %q", r.Body)
	}
}

func TestOtherSubcommandsDecline(t *testing.T) {
	for _, argv := range [][]string{{"composer", "show"}, {"composer", "outdated"}, {"composer", "--version"}, {"composer"}} {
		if _, err := New().Aggressive(context.Background(), stderrInput(argv, "Loading composer repositories\n", 0)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v err = %v", argv, err)
		}
	}
	if _, err := New().Aggressive(context.Background(), stderrInput([]string{"composer", "install"}, "something unrelated\n", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("unrecognised err = %v", err)
	}
}

func TestRelaxedKeepsDiagnostics(t *testing.T) {
	raw, code := load(t, "conflict")
	r, err := New().Relaxed(context.Background(), stderrInput([]string{"composer", "show"}, raw, code))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Body), "\n\n") || !strings.Contains(string(r.Body), "Problem 2") || !r.FoldStderr {
		t.Errorf("relaxed body wrong: %q", r.Body)
	}
	if _, err := New().Relaxed(context.Background(), stderrInput([]string{"composer", "show"}, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("empty err = %v", err)
	}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "composer" {
		t.Fatal("wrong command")
	}
}
