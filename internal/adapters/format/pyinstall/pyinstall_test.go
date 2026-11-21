package pyinstall

import (
	"context"
	"errors"
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

func find(t *testing.T, cmd string, subs ...string) *Formatter {
	t.Helper()
	for _, f := range All() {
		m := f.Descriptor()
		if m.Command == cmd && strings.Join(m.Subcommands, " ") == strings.Join(subs, " ") {
			return f
		}
	}
	t.Fatalf("no formatter for %s %v", cmd, subs)
	return nil
}

func TestRealFixtures(t *testing.T) {
	tests := []struct {
		name, fixture string
		f             *Formatter
		argv          []string
		keep, drop    []string
		smaller       bool
	}{
		{"uv sync folds packages and drops progress", "uv_sync", find(t, "uv", "sync"), []string{"uv", "sync"},
			[]string{"Resolved 15 packages\n", "Installed 14 packages\n", "+ annotated-types==0.8.0 certifi==2026.11.20", "urllib3==2.8.0", "progress/comment lines omitted"},
			[]string{"Downloading", "Downloaded", "552ms", "Using CPython"}, true},
		{"uv sync up to date", "uv_sync_again", find(t, "uv", "sync"), []string{"uv", "sync"},
			[]string{"Resolved 15 packages", "Checked 14 packages"}, []string{"6ms"}, false},
		{"uv add", "uv_add", find(t, "uv", "add"), []string{"uv", "add", "httpx"},
			[]string{"+ anyio==4.16.0 h11==0.16.0 httpcore==1.0.9 httpx==0.28.1"}, nil, false},
		{"uv add failure keeps the cause chain and hint", "uv_add_bad", find(t, "uv", "add"), []string{"uv", "add", "x"},
			[]string{"error: Failed to add dependencies", "No solution found when resolving dependencies", "hint:"}, nil, false},
		{"uv pip install failure keeps the real exception", "uv_pip_install_bad", find(t, "uv", "pip", "install"), []string{"uv", "pip", "install", "x"},
			[]string{"error: Failed to build `requests==0.14.2`", "ModuleNotFoundError: No module named 'cgi'", "…+"}, nil, true},
		{"uv pip uninstall", "uv_pip_uninstall", find(t, "uv", "pip", "uninstall"), []string{"uv", "pip", "uninstall", "flask"},
			[]string{"Uninstalled 1 package", "- flask==3.1.3"}, []string{"3ms"}, false},
		{"poetry install", "poetry_install", find(t, "poetry", "install"), []string{"poetry", "install"},
			[]string{"Package operations: 9 installs, 0 updates, 0 removals", "+ certifi==2026.11.20 charset-normalizer==3.5.2", "urllib3==2.8.0"},
			[]string{"  - Installing"}, true},
		{"poetry add failure", "poetry_add_bad", find(t, "poetry", "add"), []string{"poetry", "add", "x"},
			[]string{"Could not find a matching version of package this-package-does-not-exist-zzz"}, nil, false},
		{"pdm install drops banner and folds checkmarks", "pdm_install", find(t, "pdm", "install"), []string{"pdm", "install"},
			[]string{"Synchronizing working set with resolved packages: 9 to add", "+ certifi 2026.11.20 mdurl 0.1.2", "All complete! 9/9", "WARNING: Lockfile does not exist"},
			[]string{"!!", "Please be careful", "✔ Install"}, true},
		{"pdm add failure keeps the error", "pdm_add_bad", find(t, "pdm", "add"), []string{"pdm", "add", "x"},
			[]string{"[CandidateNotFound]: Unable to find candidates for this-package-does-not-exist-zzz"}, nil, false},
		{"pip-compile keeps pins, drops via comments", "pipcompile", find(t, "pip-compile"), []string{"pip-compile", "requirements.in"},
			[]string{"certifi==2026.11.20 charset-normalizer==3.5.2", "WARNING: --strip-extras", "comment lines omitted"}, []string{"# via"}, true},
		{"pip-compile failure collapses the traceback to the exception", "pipcompile_bad", find(t, "pip-compile"), []string{"pip-compile", "bad.in"},
			[]string{"ERROR: Could not find a version that satisfies the requirement this-package-does-not-exist-zzz", "Traceback: …+", "ResolutionImpossible", "The above exception was the direct cause"},
			[]string{"self._add_to_criteria"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := load(t, tt.fixture)
			// uv and pip-compile write to stderr; feed it there like the real thing.
			in := format.Input{Argv: tt.argv, Stderr: strings.NewReader(raw), Stdout: strings.NewReader("")}
			out, err := tt.f.Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
			if !out.FoldStderr {
				t.Error("stderr consumed but FoldStderr false")
			}
			for _, k := range tt.keep {
				if !strings.Contains(body, k) {
					t.Errorf("missing %q", k)
				}
			}
			for _, d := range tt.drop {
				if strings.Contains(body, d) {
					t.Errorf("unexpected %q", d)
				}
			}
			if tt.smaller && len(body) >= len(raw) {
				t.Errorf("render %d not smaller than raw %d", len(body), len(raw))
			}
		})
	}
}

func TestVerboseDeclines(t *testing.T) {
	f := find(t, "uv", "sync")
	_, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"uv", "sync", "-v"}, Stderr: strings.NewReader("DEBUG x\n")})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err = %v", err)
	}
}

func TestNameListCapIsAnnounced(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Installed 60 packages in 1s\n")
	for i := 0; i < 60; i++ {
		sb.WriteString(" + pkg" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "==1.0\n")
	}
	out, err := find(t, "uv", "sync").Aggressive(context.Background(), format.Input{Argv: []string{"uv", "sync"}, Stderr: strings.NewReader(sb.String())})
	if err != nil || !strings.Contains(string(out.Body), "…+20 more") || !strings.Contains(string(out.Body), "Installed 60 packages\n") {
		t.Fatalf("err=%v body=%q", err, out.Body)
	}
}
