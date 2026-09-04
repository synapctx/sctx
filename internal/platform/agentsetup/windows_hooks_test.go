package agentsetup

// A binary path containing a space — what os.Executable() commonly returns on
// Windows (`C:\Users\Jane Doe\...\sctx.exe`) — must round-trip through every
// hook installer that keeps its own config file: quoted going IN, so the
// shell that reads the entry does not split it on the space, and unquoted
// coming back OUT of Inspect, since WiredTo feeds os.Stat and (for Claude)
// `sctx doctor`'s own exec — neither goes through a shell.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookInstallersRoundTripABinaryPathWithASpace(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "Jane Doe", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "sctx")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("cursor", func(t *testing.T) {
		if _, err := InstallCursorHooks(home, binary); err != nil {
			t.Fatal(err)
		}
		st, err := InspectCursorHooks(home, binary)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Installed || st.Stale {
			t.Fatalf("want installed and current, got %+v", st)
		}
		if st.WiredTo != binary {
			t.Errorf("WiredTo = %q, want %q (unquoted)", st.WiredTo, binary)
		}
	})

	t.Run("copilot", func(t *testing.T) {
		if _, err := InstallCopilotHooks(home, binary); err != nil {
			t.Fatal(err)
		}
		st, err := InspectCopilotHooks(home, binary)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Installed || st.Stale {
			t.Fatalf("want installed and current, got %+v", st)
		}
		if st.WiredTo != binary {
			t.Errorf("WiredTo = %q, want %q (unquoted)", st.WiredTo, binary)
		}
	})

	t.Run("codex", func(t *testing.T) {
		if _, err := InstallCodexHooks(home, binary); err != nil {
			t.Fatal(err)
		}
		st, err := InspectCodexHooks(home, binary)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Installed || st.Stale {
			t.Fatalf("want installed and current, got %+v", st)
		}
		if st.WiredTo != binary {
			t.Errorf("WiredTo = %q, want %q (unquoted)", st.WiredTo, binary)
		}
	})

	t.Run("droid", func(t *testing.T) {
		if _, err := InstallDroidHooks(home, binary); err != nil {
			t.Fatal(err)
		}
		st, err := InspectDroidHooks(home, binary)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Installed || st.Stale {
			t.Fatalf("want installed and current, got %+v", st)
		}
	})

	t.Run("claude", func(t *testing.T) {
		if _, err := InstallHooks(home, binary); err != nil {
			t.Fatal(err)
		}
		_, states, err := InspectHooks(home, binary)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range states {
			if !s.Installed {
				t.Errorf("hook %s(%s) not installed", s.Event, s.Matcher)
			}
		}
		if hb, ok := HookBinary(home, claudeAgent()); !ok || hb != binary {
			t.Errorf("HookBinary = (%q, %v), want (%q, true) unquoted", hb, ok, binary)
		}
	})
}
