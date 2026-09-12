package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/agentsetup"
	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/pkg/agentdoc"
)

// writeFakeSctxAt writes an executable literally named "sctx" (or
// "sctx.exe") inside dir, answering `<path> version` with versionOutput —
// mirroring agentsetup's own fakeSctxAt fixture, duplicated here because that
// helper is unexported in a different package.
func writeFakeSctxAt(t *testing.T, dir, versionOutput string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary staleness tests need a POSIX shell script")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sctx")
	script := "#!/bin/sh\necho '" + versionOutput + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake sctx binary: %v", err)
	}
	return path
}

// TestPrintHookStatusReportsStaleForNonClaudeAgent guards Fix 1: before it,
// printHookStatus inspected Claude Code only (gated by
// `if hasAgent(st, "claude")`), so a stale Codex hook never affected
// hooksOK or appeared in this report at all.
//
// MUTATION CAUGHT: re-adding the claude-only gate, or dropping the "codex"
// case from printAgentHookStatus's dispatch, makes this see neither the
// [stale] marker nor a false return.
func TestPrintHookStatusReportsStaleForNonClaudeAgent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	oldBinary := writeFakeSctxAt(t, filepath.Join(binDir, "old"), "sctx 0.6.1")
	newBinary := writeFakeSctxAt(t, filepath.Join(binDir, "new"), "sctx 0.7.0")

	if _, err := agentsetup.InstallCodexHooks(home, oldBinary); err != nil {
		t.Fatalf("InstallCodexHooks: %v", err)
	}

	codexAgent, ok := agentdoc.AgentByID("codex")
	if !ok {
		t.Fatal("codex missing from agentdoc.KnownAgents")
	}
	targets := []agentsetup.Target{{Agent: codexAgent}}

	var buf bytes.Buffer
	if ok := printHookStatus(&buf, home, targets, newBinary); ok {
		t.Fatalf("want hooksOK=false for a stale non-Claude hook, got true:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "[stale]") {
		t.Errorf("stale Codex hook was not reported [stale]:\n%s", buf.String())
	}
}

// TestSetupInstallRewiresStaleHookToNewestOnPathNotToItself guards Fix 2: it
// must fail against the pre-fix code, where `binary` was always
// os.Executable() and a hook was rewired toward whatever is running `setup`
// now, never toward a newer release elsewhere on PATH.
//
// MUTATION CAUGHT: removing `binary = agentsetup.NewestOnPath(...)` in
// runSetup (or reverting NewestOnPath to always return runningBinary) makes
// the installed hook name the (older) running test binary instead of the
// newer PATH copy.
func TestSetupInstallRewiresStaleHookToNewestOnPathNotToItself(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows: os.UserHomeDir reads USERPROFILE, not HOME
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	pathDir := t.TempDir()
	newerBinary := writeFakeSctxAt(t, pathDir, "sctx 9.9.9")
	t.Setenv("PATH", pathDir)

	// `version` is the package-level build version runSetup feeds to
	// NewestOnPath as the running binary's own version — pinned OLDER than the
	// fake release on PATH, and restored so no other test observes it.
	oldVersion := version
	version = "0.1.0"
	t.Cleanup(func() { version = oldVersion })

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{SpoolDir: filepath.Join(home, ".config", "sctx", "spool")}
	runSetup(cfg, []string{"--install"})

	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("Claude settings.json was not written: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, newerBinary) {
		t.Errorf("hook was not wired to the newer PATH copy %s:\n%s", newerBinary, got)
	}
	if strings.Contains(got, self) {
		t.Errorf("hook was wired to the (older) running binary %s instead of the newer PATH copy:\n%s", self, got)
	}
}

// TestSetupInstallDevBuildInstallsItselfDespiteNewerReleaseOnPath guards the
// half of Fix 2 that must NOT change: a developer's own dev build running
// `setup --install` must keep installing itself, never be redirected to a
// released copy elsewhere on PATH — see NewestOnPath's runningVersion guard.
//
// MUTATION CAUGHT: dropping that guard (or inverting the dev-build check)
// makes the installed hook name the newer PATH release instead of this
// process's own path.
func TestSetupInstallDevBuildInstallsItselfDespiteNewerReleaseOnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	pathDir := t.TempDir()
	writeFakeSctxAt(t, pathDir, "sctx 9.9.9")
	t.Setenv("PATH", pathDir)

	oldVersion := version
	version = "dev"
	t.Cleanup(func() { version = oldVersion })

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{SpoolDir: filepath.Join(home, ".config", "sctx", "spool")}
	runSetup(cfg, []string{"--install"})

	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("Claude settings.json was not written: %v", err)
	}
	if !strings.Contains(string(raw), self) {
		t.Errorf("dev build did not install itself:\n%s", raw)
	}
}
