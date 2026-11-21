package pip

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Captures from the real pip binary (pip 26, non-TTY, merged streams).
func TestRealInstallFixtures(t *testing.T) {
	tests := []struct {
		name, fixture string
		exit          int
		keep, drop    []string
	}{
		{"install", "pip_install", 0, []string{"Successfully installed certifi-2026.11.20", "rich-15.0.0", "…+"},
			[]string{"Downloading", "Collecting", "━"}},
		{"already satisfied", "pip_install_again", 0, nil, []string{"Requirement already satisfied"}},
		{"failure keeps every ERROR line", "pip_install_bad", 1,
			[]string{"ERROR: Could not find a version that satisfies the requirement this-package-does-not-exist-zzz (from versions: none)",
				"ERROR: No matching distribution found for this-package-does-not-exist-zzz"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := load(t, tt.fixture)
			out, err := New().Aggressive(context.Background(), input([]string{"pip", "install", "x"}, "", raw, tt.exit))
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			for _, k := range tt.keep {
				if !strings.Contains(body, k) {
					t.Errorf("missing %q in %q", k, body)
				}
			}
			for _, d := range tt.drop {
				if strings.Contains(body, d) {
					t.Errorf("unexpected %q", d)
				}
			}
		})
	}
}

// A failed wheel build explains itself in lines that do not start with
// "ERROR"; dropping them left only "Failed building wheel".
func TestFailedBuildKeepsBackendOutput(t *testing.T) {
	stderr := strings.Join([]string{
		"Collecting badpkg",
		"  Downloading badpkg-1.0.tar.gz (10 kB)",
		"  Installing build dependencies: started",
		"  Preparing metadata (pyproject.toml): finished with status 'error'",
		"  error: subprocess-exited-with-error",
		"  ",
		"  × Preparing metadata did not run successfully.",
		"  │ exit code: 1",
		"  ╰─> [3 lines of output]",
		"      fatal error: 'foo.h' file not found",
		"      [end of output]",
		"ERROR: Failed to build 'badpkg' when preparing metadata",
	}, "\n")
	in := format.Input{Argv: []string{"pip", "install", "badpkg"}, Stderr: strings.NewReader(stderr), ExitCode: 1}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, k := range []string{"fatal error: 'foo.h' file not found", "× Preparing metadata did not run successfully.", "ERROR: Failed to build 'badpkg'"} {
		if !strings.Contains(body, k) {
			t.Errorf("missing %q in %q", k, body)
		}
	}
	if strings.Contains(body, "Downloading") {
		t.Errorf("progress leaked: %q", body)
	}
}
