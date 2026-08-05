package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A machine that installed sctx before the fallback was removed still has
// `--fallback legacy-wrapper` in its settings. The flag is inert, so this is not a
// correctness fix — it is that a settings file naming a tool we removed tells
// the next reader that sctx still depends on it.
func TestReinstallStripsTheRemovedFallbackFlag(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "claude")
	settings := filepath.Join(home, ".claude", "settings.json")
	write(t, settings, `{
  "theme": "dark",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/Users/x/.local/bin/sctx hook claude --fallback legacy-wrapper"}]}
    ]
  }
}`)

	if _, err := InstallHooks(home, "/Users/x/.local/bin/sctx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	got := read(t, settings)
	if strings.Contains(got, "--fallback") || strings.Contains(got, "legacy-wrapper") {
		t.Errorf("stale flag survived reinstall:\n%s", got)
	}
	if !strings.Contains(got, "sctx hook claude") {
		t.Errorf("stripping the flag removed the hook itself:\n%s", got)
	}
	// Everything else in the developer's file is theirs.
	if !strings.Contains(got, `"theme": "dark"`) {
		t.Errorf("clobbered unrelated settings:\n%s", got)
	}
}

// Another tool's hook is none of our business, and a --fallback on someone
// else's command may still be load-bearing.
func TestAnotherToolsFallbackFlagIsNotTouched(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "claude")
	settings := filepath.Join(home, ".claude", "settings.json")
	write(t, settings, `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/othertool hook claude --fallback something"}]}
    ]
  }
}`)

	if _, err := InstallHooks(home, "/Users/x/.local/bin/sctx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if got := read(t, settings); !strings.Contains(got, "othertool hook claude --fallback something") {
		t.Errorf("edited another tool's hook:\n%s", got)
	}
}

var _ = os.ReadFile
