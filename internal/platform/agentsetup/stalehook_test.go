package agentsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeSctxAt writes an executable literally named "sctx" inside parent/name/,
// answering `<path> version` with versionOutput (e.g. "sctx 0.6.1" or "sctx
// dev"). The basename MUST be exactly "sctx" — invokesSctxHook only
// recognises entries whose program token ends in "sctx"/"sctx.exe" — so each
// simulated install gets its OWN directory rather than a distinguishing
// filename.
func fakeSctxAt(t *testing.T, parent, name, versionOutput string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary staleness tests need a POSIX shell script")
	}
	dir := filepath.Join(parent, name)
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

func TestStaleHookReason(t *testing.T) {
	dir := t.TempDir()
	oldRelease := fakeSctxAt(t, dir, "old-release", "sctx 0.6.1")
	newRelease := fakeSctxAt(t, dir, "new-release", "sctx 0.7.0")
	devBuild := fakeSctxAt(t, dir, "dev-build", "sctx dev")
	missing := filepath.Join(dir, "gone", "sctx")

	cases := []struct {
		name           string
		wired          string
		running        string
		runningVer     string
		wantStale      bool
		reasonMustHave string
	}{
		{"same path is never stale", newRelease, newRelease, "sctx 0.7.0", false, ""},
		{"missing wired binary is stale", missing, newRelease, "sctx 0.7.0", true, "no longer exists"},
		{"dev build is stale once a release exists", devBuild, newRelease, "sctx 0.7.0", true, "dev build"},
		{"older release is stale", oldRelease, newRelease, "sctx 0.7.0", true, "older"},
		{"newer or equal release is not stale", newRelease, oldRelease, "sctx 0.6.1", false, ""},
		{"running a dev build never rewires on version grounds", oldRelease, devBuild, "sctx dev", false, ""},
		{"empty wired is never stale", "", newRelease, "sctx 0.7.0", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, stale := StaleHookReason(tc.wired, tc.running, tc.runningVer)
			if stale != tc.wantStale {
				t.Fatalf("stale = %v, want %v (reason=%q)", stale, tc.wantStale, reason)
			}
			if tc.wantStale && !strings.Contains(reason, tc.reasonMustHave) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.reasonMustHave)
			}
		})
	}
}

func TestVersionOlder(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"sctx 0.6.1", "sctx 0.7.0", true},
		{"sctx 0.7.0", "sctx 0.6.1", false},
		{"sctx 0.9.0", "sctx 0.10.0", true}, // numeric, not lexicographic
		{"sctx 0.7.0", "sctx 0.7.0", false},
		{"dev", "sctx 0.7.0", true}, // falls back to string compare, never panics
	}
	for _, tc := range cases {
		if got := versionOlder(tc.a, tc.b); got != tc.want {
			t.Errorf("versionOlder(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---- per-agent rewire tests: an existing hook wired to a dev build (or an
// older release) must be REPLACED in place by --install, not preserved. ----

func TestInstallHooksRewiresAStaleClaudeBinary(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "claude")
	binDir := t.TempDir()
	devBinary := fakeSctxAt(t, binDir, "dev", "sctx dev")
	release := fakeSctxAt(t, binDir, "release", "sctx 0.7.0")

	settings := filepath.Join(home, ".claude", "settings.json")
	write(t, settings, fmt.Sprintf(`{"hooks": {"PreToolUse": [
		{"matcher": "Bash", "hooks": [{"type": "command", "command": %q}]}
	]}}`, devBinary+" hook claude"))

	changed, err := InstallHooks(home, release)
	if err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if len(changed) == 0 || !strings.Contains(strings.Join(changed, "\n"), "rewired") {
		t.Fatalf("changed = %v, want a rewired message", changed)
	}
	got := read(t, settings)
	if strings.Contains(got, devBinary) {
		t.Errorf("dev binary still wired after install:\n%s", got)
	}
	if !strings.Contains(got, release+" hook claude") {
		t.Errorf("release binary not wired after install:\n%s", got)
	}

	// A second install against the SAME release binary must be a true no-op.
	changed2, err := InstallHooks(home, release)
	if err != nil {
		t.Fatalf("second InstallHooks: %v", err)
	}
	if len(changed2) != 0 {
		t.Errorf("second install changed %v, want no-op", changed2)
	}
}

func TestInstallCursorHooksRewiresAStaleBinary(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	oldRelease := fakeSctxAt(t, binDir, "old", "sctx 0.6.0")
	newRelease := fakeSctxAt(t, binDir, "new", "sctx 0.7.0")

	path := filepath.Join(home, ".cursor", "hooks.json")
	write(t, path, fmt.Sprintf(`{"version":1,"hooks":{"preToolUse":[{"command":%q,"matcher":"Shell"}]}}`, oldRelease+" hook cursor"))

	changed, err := InstallCursorHooks(home, newRelease)
	if err != nil {
		t.Fatalf("InstallCursorHooks: %v", err)
	}
	if len(changed) == 0 || !strings.Contains(changed[0], "rewired") {
		t.Fatalf("changed = %v, want a rewired message", changed)
	}
	got := read(t, path)
	if strings.Contains(got, oldRelease) || !strings.Contains(got, newRelease) {
		t.Errorf("cursor hooks.json not rewired:\n%s", got)
	}
	if n := strings.Count(got, "hook cursor"); n != 1 {
		t.Errorf("hook cursor appears %d times, want exactly 1:\n%s", n, got)
	}
}

func TestInstallCopilotHooksRewiresAStaleBinary(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	devBinary := fakeSctxAt(t, binDir, "dev", "sctx dev")
	release := fakeSctxAt(t, binDir, "release", "sctx 0.7.0")

	path := filepath.Join(home, ".copilot", "hooks", copilotHookFileName)
	if err := writeJSONObject(path, copilotHookDoc(devBinary)); err != nil {
		t.Fatal(err)
	}

	changed, err := InstallCopilotHooks(home, release)
	if err != nil {
		t.Fatalf("InstallCopilotHooks: %v", err)
	}
	if len(changed) == 0 || !strings.Contains(changed[0], "rewired") {
		t.Fatalf("changed = %v, want a rewired message", changed)
	}
	got := read(t, path)
	if strings.Contains(got, devBinary) || !strings.Contains(got, release) {
		t.Errorf("copilot hook not rewired:\n%s", got)
	}
}

func TestInstallDroidHooksRewiresAStaleBinary(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	oldRelease := fakeSctxAt(t, binDir, "old", "sctx 0.6.0")
	newRelease := fakeSctxAt(t, binDir, "new", "sctx 0.7.0")

	path := filepath.Join(home, ".factory", "hooks.json")
	write(t, path, fmt.Sprintf(`{"PreToolUse": [
		{"matcher": "Execute", "hooks": [{"type": "command", "command": %q}]}
	]}`, oldRelease+" hook droid"))

	changed, err := InstallDroidHooks(home, newRelease)
	if err != nil {
		t.Fatalf("InstallDroidHooks: %v", err)
	}
	if len(changed) == 0 || !strings.Contains(changed[0], "rewired") {
		t.Fatalf("changed = %v, want a rewired message", changed)
	}
	got := read(t, path)
	if strings.Contains(got, oldRelease) || !strings.Contains(got, newRelease) {
		t.Errorf("droid hook not rewired:\n%s", got)
	}
}

func TestInstallCodexHooksRewiresAStaleBinary(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	oldRelease := fakeSctxAt(t, binDir, "old", "sctx 0.6.0")
	newRelease := fakeSctxAt(t, binDir, "new", "sctx 0.7.0")

	// Seed a config.toml with the block as InstallCodexHooks itself would have
	// written it for oldRelease, so the ONLY drift is the version.
	path := filepath.Join(home, ".codex", "config.toml")
	seeded := codexHooksBegin + "\n" + codexHookBody(oldRelease) + codexHooksEnd + "\n"
	write(t, path, seeded)

	changed, err := InstallCodexHooks(home, newRelease)
	if err != nil {
		t.Fatalf("InstallCodexHooks: %v", err)
	}
	if len(changed) == 0 || !strings.Contains(changed[0], "rewired") {
		t.Fatalf("changed = %v, want a rewired message: %v", changed, changed)
	}
	got := read(t, path)
	if strings.Contains(got, oldRelease) || !strings.Contains(got, newRelease) {
		t.Errorf("codex hook not rewired:\n%s", got)
	}
}

func TestInstallPluginRewiresAStaleBinary(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "kilocode")
	binDir := t.TempDir()
	devBinary := fakeSctxAt(t, binDir, "dev", "sctx dev")
	release := fakeSctxAt(t, binDir, "release", "sctx 0.7.0")

	path := filepath.Join(home, filepath.FromSlash(a.PluginPath))
	write(t, path, SctxPluginSource(devBinary, a.ID))

	changed, err := InstallPlugin(home, a, release)
	if err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	if len(changed) == 0 || !strings.Contains(changed[0], "rewired") {
		t.Fatalf("changed = %v, want a rewired message", changed)
	}
	got := read(t, path)
	if strings.Contains(got, devBinary) || !strings.Contains(got, release) {
		t.Errorf("plugin SCTX_BINARY not rewired:\n%s", got)
	}
}
