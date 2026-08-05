package agentsetup

import (
	"github.com/synapctx/sctx/pkg/agentdoc"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// configure makes an agent look installed the only way we accept: by leaving its
// own configuration directory behind.
func configure(t *testing.T, home, agentID string) Agent {
	t.Helper()
	a, ok := agentdoc.AgentByID(agentID)
	if !ok {
		t.Fatalf("unknown agent %q", agentID)
	}
	if err := os.MkdirAll(filepath.Join(home, a.Detect[0]), 0o755); err != nil {
		t.Fatal(err)
	}
	return a
}

// THE property the multi-agent work exists for. A customer using Codex must not
// end up with a ~/.claude they never asked for, and vice versa — writing to the
// popular agent instead of the installed one produces a file nothing reads and
// an agent nothing told, which is the exact failure this package is here to fix.
func TestOnlyConfiguredAgentsAreTouched(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "codex")

	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Error("created ~/.claude on a machine with no Claude Code")
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Error("created ~/.gemini on a machine with no Gemini CLI")
	}
	body, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatalf("the configured agent was not taught: %v", err)
	}
	if !strings.Contains(string(body), agentdoc.BeginMarker) {
		t.Error("no managed block in the Codex instruction file")
	}
}

// Detecting nothing must not be reported as success, and must not write. A
// machine with no agent is not "already set up" — it is a machine we cannot
// help yet, and saying so is the only actionable answer.
func TestNoAgentDetectedIsNeitherCompleteNorDestructive(t *testing.T) {
	home := t.TempDir()
	st, err := Inspect(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Detected() || st.Complete() {
		t.Error("an empty machine must report neither detected nor complete")
	}
	if len(st.Searched) == 0 {
		t.Error("must report what it looked for; 'none found' is useless alone")
	}
	if _, err := Install(home, nil); err == nil {
		t.Error("Install must refuse rather than pick an agent for the user")
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Errorf("Install wrote to a machine with no agent: %v", entries)
	}
}

// Agents that resolve @-includes get short sidecar files; the rest get the text
// itself. Writing "@SCTX.md" into a file that does not resolve includes is a
// line that silently loads nothing — the failure mode is indistinguishable from
// success, which is how this whole class of bug hides.
func TestNonIncludingAgentsGetTheTextNotAReference(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "claude")
	configure(t, home, "codex")
	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}

	claude, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if !strings.Contains(string(claude), "@SCTX.md") {
		t.Error("Claude Code should reference the sidecar, not inline it")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "SCTX.md")); err != nil {
		t.Errorf("sidecar not written for an include-capable agent: %v", err)
	}

	codex, _ := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if strings.Contains(string(codex), "@SCTX.md") {
		t.Error("wrote an @-include into an agent that does not resolve them — it loads nothing")
	}
	if !strings.Contains(string(codex), "token-optimized command output") {
		t.Errorf("Codex got neither the text nor a working reference:\n%s", codex)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "claude")
	if _, err := Install(home, nil); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Errorf("second install changed %v, want nothing", changed)
	}
	root, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if n := strings.Count(string(root), agentdoc.BeginMarker); n != 1 {
		t.Errorf("block appears %d times after two installs", n)
	}
}

// The markers are what make this survivable across releases: templates change,
// and without them the only options are appending a second stale copy or
// rewriting a file we do not own.
func TestStaleInstructionsAreDetectedAndReplacedInPlace(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "codex")
	root := filepath.Join(home, ".codex", "AGENTS.md")
	write(t, root, "# My rules\n\nBe terse.\n\n"+agentdoc.BeginMarker+"\nancient instructions\n"+agentdoc.EndMarker+"\n\n# After\n")

	st, err := Inspect(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Targets[0].Installed || !st.Targets[0].Stale {
		t.Fatalf("want installed+stale, got %+v", st.Targets[0])
	}

	if _, err := Install(home, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(root)
	out := string(got)
	if strings.Contains(out, "ancient instructions") {
		t.Error("stale block survived a reinstall")
	}
	// The developer's content on BOTH sides must survive — an in-place replace
	// that only preserved the prefix would silently delete everything after.
	if !strings.HasPrefix(out, "# My rules\n\nBe terse.\n") || !strings.Contains(out, "# After") {
		t.Errorf("content outside the block was altered:\n%s", out)
	}
	if n := strings.Count(out, agentdoc.BeginMarker); n != 1 {
		t.Errorf("block appears %d times", n)
	}
}

// A hand-edited sidecar was customised on purpose; replacing it silently is
// worse than leaving it slightly stale.
func TestSidecarsAreNeverOverwritten(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "claude")
	mine := "# my own notes, do not clobber\n"
	write(t, filepath.Join(home, ".claude", "SCTX.md"), mine)

	if _, err := Install(home, nil, agentdoc.SctxDoc); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".claude", "SCTX.md"))
	if string(got) != mine {
		t.Errorf("existing content was overwritten:\n%s", got)
	}
}

// The instruction file is the developer's and its opening lines are theirs.
// Appending must survive a file with no trailing newline, or the marker lands on
// the end of their last sentence: their instruction is rewritten AND ours never
// parses.
func TestAppendPreservesTheDevelopersContentByteForByte(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "codex")
	original := "# Mine\nalways use tabs" // no trailing newline, on purpose
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), original)

	if _, err := Install(home, nil, agentdoc.SctxDoc); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if !strings.HasPrefix(string(got), original+"\n") {
		t.Fatalf("the developer's content was altered:\n%q", got)
	}
	if !strings.Contains(string(got), "\n"+agentdoc.BeginMarker) {
		t.Errorf("marker did not land on its own line:\n%s", got)
	}
}

// An opening marker with no close is a truncated or hand-mangled file. Reading
// it as installed would leave the agent permanently untaught with a green status.
func TestAnUnterminatedBlockCountsAsNotInstalled(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "codex")
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), "# Mine\n"+agentdoc.BeginMarker+"\nhalf a block\n")

	st, err := Inspect(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Targets[0].Installed {
		t.Error("a block with no END marker was reported as installed")
	}
	if _, err := Install(home, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if !strings.Contains(string(got), agentdoc.EndMarker) {
		t.Errorf("reinstall did not produce a well-formed block:\n%s", got)
	}
}

func TestEveryKnownAgentIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range agentdoc.KnownAgents {
		if a.ID == "" || a.Name == "" || a.Root == "" || len(a.Detect) == 0 {
			t.Errorf("agent %+v is incomplete", a)
		}
		if seen[a.ID] {
			t.Errorf("duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
		if filepath.IsAbs(a.Root) {
			t.Errorf("agent %q Root must be relative to home, got %q", a.ID, a.Root)
		}
		for _, d := range a.Detect {
			if filepath.IsAbs(d) {
				t.Errorf("agent %q Detect path must be relative to home, got %q", a.ID, d)
			}
		}
	}
}

// The first real-machine install duplicated both includes, because the file
// said `@~/.claude/SCTX.md` and detection only recognised the bare `@SCTX.md`.
// Every test before this one built its fixture with the bare form, so the shape
// that actually exists on a developer's machine was never exercised.
//
// Two loads of the same document ride in EVERY session's context, forever.
func TestAPathQualifiedIncludeIsNotDuplicated(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	claude := filepath.Join(home, a.Root)
	write(t, claude, "# Mine\n\n@~/.claude/SCTX.md\n@~/.claude/SYNAPCTX.md\n")

	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatalf("install: %v", err)
	}

	body := read(t, claude)
	for _, name := range []string{"SCTX.md", "SYNAPCTX.md"} {
		if n := strings.Count(body, name); n != 1 {
			t.Errorf("%s is referenced %d times, want 1:\n%s", name, n, body)
		}
	}
}

// …and a machine where the developer already wired both documents by hand must
// REPORT as taught. Otherwise `sctx gain` nags on every run about a correctly
// configured machine, which is how a warning gets muted.
func TestAHandConfiguredMachineReportsTaught(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	write(t, filepath.Join(home, a.Root), "@~/.claude/SCTX.md\n@~/.claude/SYNAPCTX.md\n")

	st, err := Inspect(home, []string{"acme"})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !st.Complete() {
		t.Errorf("a machine already including both documents reported incomplete: %+v", st.Targets)
	}
}

// The counterweight: our own include must NOT be read as the developer's, or
// the next install drops it from the block, empties the block, and silently
// unteaches the agent while reporting success.
func TestOurOwnIncludeDoesNotSuppressTheBlock(t *testing.T) {
	home := t.TempDir()
	a := configure(t, home, "claude")
	claude := filepath.Join(home, a.Root)
	write(t, claude, "# Mine\n")

	for i := 0; i < 2; i++ {
		if _, err := Install(home, []string{"acme"}); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	body := read(t, claude)
	if !strings.Contains(body, "@SCTX.md") || !strings.Contains(body, "@SYNAPCTX.md") {
		t.Fatalf("reinstall emptied the block:\n%s", body)
	}
	if n := strings.Count(body, "@SCTX.md"); n != 1 {
		t.Errorf("@SCTX.md appears %d times after two installs", n)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
