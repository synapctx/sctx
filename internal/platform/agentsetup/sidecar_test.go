package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

// sidecarPath is where an include-capable agent's documents live.
func sidecarPath(t *testing.T, home string, a Agent, name string) string {
	t.Helper()
	return filepath.Join(home, filepath.Dir(a.Root), name)
}

func stateOf(t *testing.T, st Status, name string) agentdoc.SidecarState {
	t.Helper()
	for _, tg := range st.Targets {
		for _, s := range tg.Sidecars {
			if s.Name == name {
				return s.State
			}
		}
	}
	t.Fatalf("no sidecar %q was inspected at all", name)
	return agentdoc.SidecarMissing
}

// THE BUG THIS MECHANISM EXISTS FOR. A correctness fix to a shipped document
// used to reach only machines that had never installed: `Install` refused to
// touch an existing sidecar because it could not tell "customised" from
// "untouched but a release behind", and `Inspect` never read sidecars at all, so
// `sctx setup` did not even report the drift.
func TestAStaleButUneditedSidecarIsUpdatedByAPlainInstall(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	orgs := []string{"acme"}

	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("first install: %v", err)
	}
	side := sidecarPath(t, home, a, agentdoc.SynapctxDoc.Name)

	// Simulate the shipped template having moved on since this file was written:
	// stamped by us, provably untouched, but no longer current.
	write(t, side, agentdoc.StampedBody("# SynapCTX\n\nlast release's text\n"))

	st, err := Inspect(home, orgs)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got := stateOf(t, st, agentdoc.SynapctxDoc.Name); got != agentdoc.SidecarStale {
		t.Fatalf("state = %v, want stale", got)
	}
	if st.Complete() {
		t.Error("a machine carrying an out-of-date document reported complete; nothing\n" +
			"would ever prompt the update")
	}

	// No --force. This is the delivery.
	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if got, want := read(t, side), agentdoc.StampedBody(agentdoc.SynapctxDoc.Body(orgs)); got != want {
		t.Errorf("a stale, unedited document was not brought current by a plain install")
	}
}

// The counterweight, and the reason the hash exists rather than a version number:
// a file the developer changed is theirs, and a plain install must not touch it.
func TestAnEditedSidecarSurvivesAPlainInstallAndIsReported(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	orgs := []string{"acme"}

	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("first install: %v", err)
	}
	side := sidecarPath(t, home, a, agentdoc.SctxDoc.Name)
	edited := read(t, side) + "\n## My own additions\n\nnever discard this\n"
	write(t, side, edited)

	st, err := Inspect(home, orgs)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got := stateOf(t, st, agentdoc.SctxDoc.Name); got != agentdoc.SidecarEdited {
		t.Fatalf("state = %v, want edited", got)
	}
	// Reported, so a human can decide — but NOT counted as incomplete, or the
	// tool nags forever about something a plain install will never resolve.
	var named bool
	for _, tg := range st.Targets {
		for _, s := range tg.Attention() {
			if s.Name == agentdoc.SctxDoc.Name {
				named = true
			}
		}
	}
	if !named {
		t.Error("an edited document is not surfaced for attention; it is invisible")
	}
	if !st.Complete() {
		t.Error("an edited document made the machine permanently incomplete, which is a\n" +
			"nag with no remedy short of discarding the developer's work")
	}

	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if read(t, side) != edited {
		t.Error("a plain install discarded the developer's own edits")
	}
}

// Every machine installed before stamping is in this state. We cannot prove the
// file is ours, so we do not touch it — but we must say so, because the one-time
// remedy is a --force the developer has to choose.
func TestAPreStampSidecarIsLeftAloneAndReported(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	orgs := []string{"acme"}

	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("first install: %v", err)
	}
	side := sidecarPath(t, home, a, agentdoc.SctxDoc.Name)
	// Exactly what a pre-stamp release wrote: the body, no provenance line.
	legacy := agentdoc.SctxDoc.Body(orgs)
	write(t, side, legacy)

	st, err := Inspect(home, orgs)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got := stateOf(t, st, agentdoc.SctxDoc.Name); got != agentdoc.SidecarUnverifiable {
		t.Fatalf("state = %v, want unverifiable", got)
	}
	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if read(t, side) != legacy {
		t.Error("an unverifiable document was overwritten without --force; it might have\n" +
			"been hand-written")
	}

	// --force is the documented one-time adoption, and it must leave the file
	// DECIDABLE so this never recurs.
	if _, err := InstallForce(home, orgs); err != nil {
		t.Fatalf("force install: %v", err)
	}
	after, err := Inspect(home, orgs)
	if err != nil {
		t.Fatalf("inspect after force: %v", err)
	}
	if got := stateOf(t, after, agentdoc.SctxDoc.Name); got != agentdoc.SidecarCurrent {
		t.Errorf("state after --force = %v, want current: the adoption did not stamp the\n"+
			"file, so it stays unverifiable forever", got)
	}
}

// A deleted document with an intact block used never to be restored: the block
// was healthy, so the target reported OK and install skipped it entirely.
func TestADeletedSidecarIsRestored(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	orgs := []string{"acme"}

	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("first install: %v", err)
	}
	side := sidecarPath(t, home, a, agentdoc.SynapctxDoc.Name)
	if err := os.Remove(side); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(home, orgs); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if _, err := os.Stat(side); err != nil {
		t.Errorf("a deleted document was not restored: %v", err)
	}
}

// A document the developer includes from somewhere else is theirs, wherever it
// lives. Managing a same-named file beside the instruction file would write a
// SECOND copy that nothing loads — in a package whose rule is that nothing is
// created speculatively.
func TestADocumentTheDeveloperIncludesThemselvesIsNotManaged(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	write(t, filepath.Join(home, a.Root), "# Mine\n\n@~/docs/SCTX.md\n")

	st, err := Inspect(home, []string{"acme"})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, tg := range st.Targets {
		for _, s := range tg.Sidecars {
			if s.Name == agentdoc.SctxDoc.Name {
				t.Errorf("we are managing %s although the developer includes their own copy", s.Name)
			}
		}
	}
	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(sidecarPath(t, home, a, agentdoc.SctxDoc.Name)); err == nil {
		t.Error("a second, unreferenced copy of SCTX.md was written beside the instruction file")
	}
	// The developer's own include must still not be duplicated into our block.
	if n := strings.Count(read(t, filepath.Join(home, a.Root)), agentdoc.SctxDoc.Name); n != 1 {
		t.Errorf("SCTX.md referenced %d times, want 1", n)
	}
}
