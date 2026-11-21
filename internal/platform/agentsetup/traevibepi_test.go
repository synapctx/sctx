package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

func TestInstallTraeHooks(t *testing.T) {
	const bin = "/opt/sctx/bin/sctx"
	t.Run("fresh install, idempotent, preserves siblings", func(t *testing.T) {
		home := t.TempDir()
		path := traeConfigPath(home)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"custom":{"keep":true},"hooks":{"PostToolUse":[{"matcher":"WriteFile","hooks":[]}]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := InstallTraeHooks(home, bin); err != nil {
			t.Fatal(err)
		}
		st, err := InspectTraeHooks(home, bin)
		if err != nil || !st.Installed || st.Stale {
			t.Fatalf("status = %+v err %v", st, err)
		}
		raw, _ := os.ReadFile(path)
		for _, want := range []string{`"version"`, `"keep"`, `"WriteFile"`, `"RunCommand"`, bin + " hook trae"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("missing %s in %s", want, raw)
			}
		}
		again, err := InstallTraeHooks(home, bin)
		if err != nil || len(again) != 0 {
			t.Fatalf("second install changed %v err %v", again, err)
		}
	})
	t.Run("unknown version is left alone", func(t *testing.T) {
		home := t.TempDir()
		path := traeConfigPath(home)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		const body = `{"version":2}`
		_ = os.WriteFile(path, []byte(body), 0o600)
		if _, err := InstallTraeHooks(home, bin); err == nil {
			t.Fatal("expected refusal")
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Fatalf("file modified: %s", got)
		}
	})
}

func TestInstallVibeHooks(t *testing.T) {
	const bin = "/opt/sctx/bin/sctx"
	home := t.TempDir()
	path := vibeConfigPath(home)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	const user = "# mine\n[[hooks]]\nname = \"other\"\ntype = \"pre_tool\"\nmatch = \"bash\"\ncommand = \"x\"\n"
	_ = os.WriteFile(path, []byte(user), 0o600)

	if _, err := InstallVibeHooks(home, bin); err != nil {
		t.Fatal(err)
	}
	st, err := InspectVibeHooks(home, bin)
	if err != nil || !st.Installed || st.Stale {
		t.Fatalf("status = %+v err %v", st, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), user) || !strings.Contains(string(raw), `command = "`+bin+` hook vibe"`) {
		t.Fatalf("file = %s", raw)
	}
	if again, err := InstallVibeHooks(home, bin); err != nil || len(again) != 0 {
		t.Fatalf("second install changed %v err %v", again, err)
	}
	// A hand edit inside the block is reported stale, not silently trusted.
	edited := strings.Replace(string(raw), "timeout = 10.0", "timeout = 99.0", 1)
	_ = os.WriteFile(path, []byte(edited), 0o600)
	if st, _ := InspectVibeHooks(home, bin); !st.Stale {
		t.Fatalf("edited block not stale: %+v", st)
	}
}

func TestInstallPiPlugin(t *testing.T) {
	const bin = "/opt/sctx/bin/sctx"
	home := t.TempDir()
	a, ok := agentdoc.AgentByID("pi")
	if !ok {
		t.Fatal("pi row missing")
	}
	if _, err := InstallPlugin(home, a, bin); err != nil {
		t.Fatal(err)
	}
	st, err := InspectPlugin(home, a, bin)
	if err != nil || !st.OK() || st.WiredTo != bin {
		t.Fatalf("status = %+v err %v", st, err)
	}
	src, _ := os.ReadFile(st.Path)
	for _, want := range []string{`"tool_call"`, `event.toolName !== "bash"`, `"hook", "rewrite"`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("extension lacks %s", want)
		}
	}
	// A file of that name that we did not write is never replaced.
	_ = os.WriteFile(st.Path, []byte("// mine\n"), 0o644)
	if _, err := InstallPlugin(home, a, bin); err == nil {
		t.Fatal("overwrote a foreign file")
	}
}
