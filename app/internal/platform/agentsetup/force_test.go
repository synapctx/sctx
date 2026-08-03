package agentsetup

import (
	"path/filepath"
	"strings"
	"testing"
)

// The default refusal is the important half: a developer who edited SCTX.md
// edited it on purpose, and silently replacing that is worse than leaving it
// stale.
func TestInstallNeverRewritesAnEditedDocument(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	side := filepath.Join(home, filepath.Dir(a.Root), "SCTX.md")
	write(t, side, "# my own notes\n")

	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := read(t, side); got != "# my own notes\n" {
		t.Errorf("Install overwrote an edited document:\n%s", got)
	}
}

// …and --force is the explicit way back onto the shipped template, which is what
// makes a hand-written file from before these were generated recoverable.
func TestInstallForceRewritesIt(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	side := filepath.Join(home, filepath.Dir(a.Root), "SCTX.md")
	write(t, side, "# my own notes\n")

	if _, err := InstallForce(home, []string{"acme"}); err != nil {
		t.Fatalf("install --force: %v", err)
	}
	got := read(t, side)
	if strings.Contains(got, "my own notes") {
		t.Errorf("--force did not replace the document:\n%s", got)
	}
	if !strings.Contains(got, "token-optimized command output") {
		t.Errorf("--force wrote something other than the shipped template:\n%s", got)
	}
}

// A developer who already includes both documents by path must not be given an
// empty managed block for their trouble — including on --force, which is
// exactly when it would appear.
func TestNoEmptyBlockIsLeftBehind(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	claude := filepath.Join(home, a.Root)
	write(t, claude, "# Mine\n\n@~/.claude/SCTX.md\n@~/.claude/SYNAPCTX.md\n")

	for i := 0; i < 2; i++ {
		if _, err := InstallForce(home, []string{"acme"}); err != nil {
			t.Fatalf("install --force %d: %v", i, err)
		}
	}
	got := read(t, claude)
	if strings.Contains(got, BeginMarker) {
		t.Errorf("left an empty managed block behind:\n%s", got)
	}
	if !strings.Contains(got, "# Mine") || strings.Count(got, "SCTX.md") != 1 {
		t.Errorf("the developer's own file was damaged:\n%s", got)
	}
}
