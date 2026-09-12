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

// TestNewestOnPath exercises the rewire TARGET fix: before it, a rewire
// always pointed at runningBinary, which let a developer's own OLDER copy
// re-pin every hook to itself even with a newer release sitting on PATH.
func TestNewestOnPath(t *testing.T) {
	dir := t.TempDir()
	oldRelease := fakeSctxAt(t, dir, "old-release", "sctx 0.6.1")
	newRelease := fakeSctxAt(t, dir, "new-release", "sctx 0.7.0")
	devBuild := fakeSctxAt(t, dir, "dev-build", "sctx dev")
	pathEnv := strings.Join([]string{filepath.Dir(oldRelease), filepath.Dir(newRelease), filepath.Dir(devBuild)}, string(filepath.ListSeparator))

	t.Run("a newer release on PATH wins over the older running binary", func(t *testing.T) {
		// MUTATION CAUGHT: reverting to `return runningBinary` unconditionally
		// (the pre-fix behaviour) makes this assert oldRelease instead.
		//
		// Compared via samePath, not string equality: binaries.OnPath resolves
		// symlinks (e.g. macOS's /var -> /private/var), so the PATH copy it
		// returns is not always byte-identical to the path fakeSctxAt handed
		// back, even though it names the same file.
		got := NewestOnPath(pathEnv, "sctx", oldRelease, "sctx 0.6.1")
		if !samePath(got, newRelease) {
			t.Errorf("got %q, want the newer release %q", got, newRelease)
		}
	})

	t.Run("running binary already the newest is returned byte-for-byte unchanged", func(t *testing.T) {
		// This is the "existing fixtures still pass" guarantee: when nothing on
		// PATH beats the running binary, the result must be IT, not merely an
		// equally-current alternative.
		got := NewestOnPath(pathEnv, "sctx", newRelease, "sctx 0.7.0")
		if got != newRelease {
			t.Errorf("got %q, want runningBinary %q unchanged", got, newRelease)
		}
	})

	t.Run("a dev build never loses itself to a release on PATH", func(t *testing.T) {
		// MUTATION CAUGHT: dropping the runningVersion dev-build guard would
		// redirect a developer's own checkout to newRelease here.
		got := NewestOnPath(pathEnv, "sctx", devBuild, "sctx dev")
		if got != devBuild {
			t.Errorf("got %q, want the dev build to install itself: %q", got, devBuild)
		}
	})

	t.Run("an unresolvable PATH falls back to the running binary", func(t *testing.T) {
		got := NewestOnPath("", "sctx", oldRelease, "sctx 0.6.1")
		if got != oldRelease {
			t.Errorf("got %q, want the running binary unchanged when PATH resolves nothing: %q", got, oldRelease)
		}
	})

	t.Run("a dev-only PATH falls back to the running binary", func(t *testing.T) {
		devOnlyPath := filepath.Dir(devBuild)
		got := NewestOnPath(devOnlyPath, "sctx", oldRelease, "sctx 0.6.1")
		if got != oldRelease {
			t.Errorf("got %q, want the running binary unchanged when nothing on PATH is a usable release: %q", got, oldRelease)
		}
	})
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

// Version comparison must work across the TWO PRODUCERS that feed it, and must
// survive the 0.9 -> 0.10 rollover.
//
// The strings compared here come from different places: `binaries.VersionOf`
// returns the CLI's own output ("sctx v0.9.0"), the `main.version` ldflag is the
// bare tag ("v0.9.0"), and an off-tag build adds a git suffix. parseVersionNumbers
// previously Atoi'd each dot-separated part, so the leading "v" failed the parse
// for EVERY real version and versionOlder silently degraded to comparing raw
// strings. That is lexicographic: correct for v0.8.0 < v0.9.0 by luck, and wrong
// for v0.9.0 < v0.10.0, because "0.10" sorts before "0.9".
//
// MUTATION THIS CATCHES: dropping the "v" strip or the leading-digit-run scan.
// Either sends every comparison back through the string fallback, where the
// v0.10.0 row fails — i.e. the first release after v0.9 stops all hook rewiring.
func TestVersionOlderAcrossProducerFormatsAndTheTenRollover(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		{"bare vs CLI output, the real NewestOnPath call", "v0.8.0", "sctx v0.9.0", true},
		{"CLI output vs bare, the real StaleHookReason call", "sctx v0.8.0", "v0.9.0", true},
		{"both bare", "v0.8.0", "v0.9.0", true},
		{"both CLI output", "sctx v0.8.0", "sctx v0.9.0", true},
		{"git suffix is older than its own release", "sctx v0.8.0-1-gbb9c1f8", "v0.9.0", true},
		{"THE ROLLOVER: 0.9 is older than 0.10", "v0.9.0", "v0.10.0", true},
		{"THE ROLLOVER, reversed: 0.10 is not older than 0.9", "v0.10.0", "v0.9.0", false},
		{"0.9.9 is older than 0.10.0", "sctx v0.9.9", "v0.10.0", true},
		{"1.0.0 is not older than 0.99.0", "v1.0.0", "v0.99.0", false},
		{"equal is not older", "v0.9.0", "sctx v0.9.0", false},
		{"patch rollover", "v0.9.9", "v0.9.10", true},
		{"a release is newer than its own pre-release build", "v0.9.0-rc1", "v0.9.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionOlder(tc.a, tc.b); got != tc.want {
				t.Errorf("versionOlder(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// The parse must actually SUCCEED on the formats we really ship, not merely
// produce a usable answer through the string fallback.
//
// MUTATION THIS CATCHES: a parse that returns ok=false for real versions. That
// is invisible in versionOlder's result for adjacent versions and catastrophic
// at a rollover, so it is asserted directly rather than through a comparison.
func TestParseVersionNumbersSucceedsOnTheFormatsWeShip(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []int
	}{
		{"v0.9.0", []int{0, 9, 0}},
		{"sctx v0.9.0", []int{0, 9, 0}},
		{"sctx v0.8.0-1-gbb9c1f8", []int{0, 8, 0}},
		{"v0.10.0", []int{0, 10, 0}},
		{"0.9.0", []int{0, 9, 0}},
		{"v1.2.beta", []int{1, 2}},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseVersionNumbers(tc.in)
			if !ok {
				t.Fatalf("parseVersionNumbers(%q) reported not-ok; every shipped format must parse, or comparison degrades to lexicographic", tc.in)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseVersionNumbers(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseVersionNumbers(%q) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}
