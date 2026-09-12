package hook

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFakeSctxOnPath writes an executable literally named "sctx" inside
// dir, answering `<path> version` with versionOutput — the same fixture
// shape agentsetup's own stalehook_test.go uses, duplicated here because
// that helper is unexported in a different package.
func writeFakeSctxOnPath(t *testing.T, dir, versionOutput string) string {
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

// TestStaleHookVersionNoticeFiresOnceWithinWindow guards Fix 3: the notice
// must appear once when a newer release is on PATH, and go silent on the
// very next call because MarkNoticeIfDue just recorded that check.
//
// MUTATION CAUGHT: skipping the MarkNoticeIfDue gate (checking staleness on
// every call) makes the second call speak again instead of staying silent;
// inverting the "newest == running" comparison makes the FIRST call silent
// instead.
func TestStaleHookVersionNoticeFiresOnceWithinWindow(t *testing.T) {
	t.Setenv("SCT__STATS_DB_PATH", filepath.Join(t.TempDir(), "stats.db"))

	pathDir := t.TempDir()
	newer := writeFakeSctxOnPath(t, pathDir, "sctx 9.9.9")
	t.Setenv("PATH", pathDir)

	notice1 := staleHookVersionNotice("0.1.0")
	if notice1 == "" {
		t.Fatal("want a notice on the first call, got none")
	}
	if !strings.Contains(notice1, newer) {
		t.Errorf("notice does not name the newer copy %s: %q", newer, notice1)
	}
	if !strings.Contains(notice1, "sctx setup --install") {
		t.Errorf("notice does not say how to fix it: %q", notice1)
	}
	if !strings.Contains(notice1, "still being compressed correctly") {
		t.Errorf("notice does not say output correctness is unaffected: %q", notice1)
	}

	notice2 := staleHookVersionNotice("0.1.0")
	if notice2 != "" {
		t.Errorf("want silence on the immediate next call (rate limited), got %q", notice2)
	}
}

// TestStaleHookVersionNoticeSilentWhenAlreadyNewest guards the "no false
// positive" half: a running binary that IS the newest non-dev copy on PATH
// (the common case, and the state every hook-envelope fixture assumes) must
// never speak.
func TestStaleHookVersionNoticeSilentWhenAlreadyNewest(t *testing.T) {
	t.Setenv("SCT__STATS_DB_PATH", filepath.Join(t.TempDir(), "stats.db"))
	t.Setenv("PATH", t.TempDir()) // nothing named sctx on PATH at all

	if notice := staleHookVersionNotice("9.9.9"); notice != "" {
		t.Errorf("want no notice when nothing on PATH beats the running version, got %q", notice)
	}
}
