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

// An include naming a directory that does not exist is followed nowhere. The
// document is REPORTED so it is visible, and no file is written — not at the
// include's path, because creating `~/docs/` on the strength of a line in a file
// we do not own is speculative, and not beside the instruction file, because a
// second copy nothing loads is worse than none.
func TestAnIncludeIntoAMissingDirectoryIsReportedAndNotWritten(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	write(t, filepath.Join(home, a.Root), "# Mine\n\n@~/docs/SCTX.md\n")

	st, err := Inspect(home, []string{"acme"})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	found := false
	for _, tg := range st.Targets {
		for _, s := range tg.Sidecars {
			if s.Name != agentdoc.SctxDoc.Name {
				continue
			}
			found = true
			if !s.Developer {
				t.Error("a document loaded by the developer's own include is not marked as theirs")
			}
			if s.State != agentdoc.SidecarUnverifiable {
				t.Errorf("state is %v, want unverifiable: we cannot write into a directory that does not exist", s.State)
			}
		}
	}
	if !found {
		t.Error("the developer's document was not reported at all — the blind spot is back")
	}

	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(sidecarPath(t, home, a, agentdoc.SctxDoc.Name)); err == nil {
		t.Error("a second, unreferenced copy of SCTX.md was written beside the instruction file")
	}
	if _, err := os.Stat(filepath.Join(home, "docs", "SCTX.md")); err == nil {
		t.Error("a directory the developer never created was created for them")
	}
	// The developer's own include must still not be duplicated into our block.
	if n := strings.Count(read(t, filepath.Join(home, a.Root)), agentdoc.SctxDoc.Name); n != 1 {
		t.Errorf("SCTX.md referenced %d times, want 1", n)
	}
}

// THE REGRESSION THIS SUITE EXISTS FOR. A hand-written `@~/.claude/SCTX.md`
// names the very path `sctx setup` would have used. It was skipped as "the
// developer's", so a stale document there was never inspected, never updated,
// and the agent reported [ok] while reading a template two releases old.
func TestADeveloperIncludeAtTheManagedPathIsUpdatedByAPlainInstall(t *testing.T) {
	home := t.TempDir()
	orgs := []string{"acme"}
	a := configure(t, home, "claude")
	write(t, filepath.Join(home, a.Root), "# Mine\n\n@~/.claude/SCTX.md\n")
	side := sidecarPath(t, home, a, agentdoc.SctxDoc.Name)
	write(t, side, agentdoc.StampedBodyFor("v0.1.0", "# sctx\n\nlast release's text\n"))

	st, err := Inspect(home, orgs, agentdoc.SctxDoc)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	s := st.Targets[0].Sidecars[0]
	if s.Path != side {
		t.Errorf("inspected %s, want the path the include actually names (%s)", s.Path, side)
	}
	if s.State != agentdoc.SidecarStale {
		t.Fatalf("state is %v, want stale", s.State)
	}
	if s.Version != "v0.1.0" {
		t.Errorf("recorded version is %q, want the one in the stamp", s.Version)
	}
	if st.Complete() {
		t.Error("a stale document reported as complete")
	}

	if _, err := InstallVersion(home, orgs, "v9.9.9", agentdoc.SctxDoc); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got, want := read(t, side), agentdoc.StampedBodyFor("v9.9.9", agentdoc.SctxDoc.Body(orgs)); got != want {
		t.Errorf("the developer's document was not brought current:\ngot  %q\nwant %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "SYNAPCTX.md")); err == nil {
		t.Error("a document that was not asked for was written")
	}
	if n := strings.Count(read(t, filepath.Join(home, a.Root)), agentdoc.SctxDoc.Name); n != 1 {
		t.Errorf("SCTX.md referenced %d times, want 1", n)
	}
}

// The same include, pointing outside the home directory. Reported, never
// written: a path in a file we do not own must not turn `--install` into a
// system-wide write.
func TestAnIncludeOutsideHomeIsNeverWritten(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir() // a sibling temp dir, so genuinely not under home
	a := configure(t, home, "claude")
	target := filepath.Join(outside, "SCTX.md")
	write(t, filepath.Join(home, a.Root), "# Mine\n\n@"+filepath.ToSlash(target)+"\n")

	st, err := Inspect(home, []string{"acme"}, agentdoc.SctxDoc)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got := st.Targets[0].Sidecars[0].State; got != agentdoc.SidecarUnverifiable {
		t.Errorf("state is %v, want unverifiable for a path outside home", got)
	}
	if _, err := Install(home, []string{"acme"}, agentdoc.SctxDoc); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("a file outside the home directory was written")
	}
}
