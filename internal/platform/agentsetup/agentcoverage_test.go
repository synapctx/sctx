package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

// WHAT A CURRENT INSTALL OF THE AGENT ACTUALLY LEAVES ON DISK IS THE ONLY THING
// DETECTION MAY KEY ON.
//
// Kilo Code shipped `~/.kilocode` when this row was written and moved to
// `~/.config/kilo` (with `.kilo`/`.kilocode` kept as legacy read paths) by
// 7.4.22. The row was never updated, so a machine running Kilo daily reported
// "not detected" and was never taught anything — the exact silent half-install
// `sctx setup` exists to catch. This test pins each root that a supported
// release still creates.
func TestKiloCodeIsDetectedFromEveryConfigRootItStillUses(t *testing.T) {
	for _, root := range []string{".config/kilo", ".kilo", ".kilocode"} {
		t.Run(root, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(root)), 0o755); err != nil {
				t.Fatal(err)
			}
			st, err := Inspect(home, nil, agentdoc.SctxDoc)
			if err != nil {
				t.Fatal(err)
			}
			var found *Target
			for i := range st.Targets {
				if st.Targets[i].ID == "kilocode" {
					found = &st.Targets[i]
				}
			}
			if found == nil {
				t.Fatalf("Kilo Code configured at %s was not detected", root)
			}
			// Written where the CURRENT release loads global instructions:
			// AGENTS.md in the config directory. `.kilocode/rules/` still loads,
			// but the binary asks for migration off it, and writing the legacy
			// path on a fresh install would age out with the next release.
			if !strings.HasSuffix(filepath.ToSlash(found.RootPath), ".config/kilo/AGENTS.md") {
				t.Errorf("instructions would be written to %s, want ~/.config/kilo/AGENTS.md", found.RootPath)
			}
		})
	}
}

// A capability field that is set but unusable is worse than one left at zero:
// the zero value is reported honestly as unmanaged, while a half-filled row
// makes `sctx setup` claim a registry it cannot find.
func TestEveryManagedMCPRowNamesItsConfigFile(t *testing.T) {
	for _, a := range agentdoc.KnownAgents {
		if a.MCP != agentdoc.MCPUnmanaged && a.MCPConfig == "" {
			t.Errorf("%s declares a managed MCP registry with no config path", a.ID)
		}
		if a.MCP == agentdoc.MCPUnmanaged && a.MCPConfig != "" {
			t.Errorf("%s names a config path but manages nothing", a.ID)
		}
	}
}

// THE INSTRUCTIONS MUST NOT PROMISE A HOOK TO AN AGENT THAT HAS NONE.
//
// `sctx setup` installs a rewrite hook for Claude Code only, but SCTX.md told
// every agent "a PreToolUse hook rewrites covered commands; do not prefix sctx
// yourself". Five of the seven agents in the table therefore received an
// instruction never to type the one thing they had to type — and the failure is
// invisible from every side: commands run fine, they are just never wrapped, so
// the saving silently never happens.
func TestInstructionsMatchWhichAgentsAreActuallyWrapped(t *testing.T) {
	body := agentdoc.SctxDoc.Body(nil)
	if !strings.Contains(body, "prefix the covered commands yourself") {
		t.Error("SCTX.md never tells an unwrapped agent to prefix sctx itself")
	}
	// Every agent has to be on the correct side of the document's split, or an
	// agent is told the opposite of what its machine does — the failure this
	// test exists for, which shipped for months against five of seven clients.
	split := strings.Index(body, "No interception point exists in")
	if split < 0 {
		t.Fatal("SCTX.md no longer separates wrapped agents from manual ones")
	}
	auto, manual := body[:split], body[split:]
	for _, a := range agentdoc.KnownAgents {
		section, where := manual, "manual"
		if a.Wrapped() {
			section, where = auto, "automatic"
		}
		if !mentionsAgent(section, a) {
			t.Errorf("%s belongs in the %s list and is not there", a.Name, where)
		}
	}
}

// A CLAIMED MECHANISM MUST ACTUALLY INSTALL, WHATEVER MECHANISM IT IS.
//
// Tested through InstallWrapping rather than against any one implementation:
// there are three (a JSON settings hook, a TOML settings hook, a plugin file)
// and the point is that a row claiming to be wrapped ends up wrapped, not which
// of the three did it. A row that claims wrapping with nothing behind it is the
// same false promise as an instruction file describing a hook nobody installed.
func TestEveryAgentClaimingAutoWrapActuallyGetsIt(t *testing.T) {
	for _, a := range agentdoc.KnownAgents {
		t.Run(a.ID, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(a.Detect[0])), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, errs := InstallWrapping(home, fakeBinary(t), agentdoc.SctxDoc); len(errs) > 0 {
				t.Fatalf("install: %v", errs)
			}
			states, err := InspectWrapping(home, fakeBinary(t), agentdoc.SctxDoc)
			if err != nil {
				t.Fatal(err)
			}
			var found *WrapState
			for i := range states {
				if states[i].AgentID == a.ID {
					found = &states[i]
				}
			}
			if found == nil {
				t.Fatalf("%s was not detected from %s", a.ID, a.Detect[0])
			}
			if a.Wrapped() != found.OK {
				t.Fatalf("row says wrapped=%v, install produced OK=%v (%s)", a.Wrapped(), found.OK, found.Detail)
			}
			if a.Wrapped() && found.Path == "" {
				t.Error("wrapped, but setup cannot say where the wiring lives")
			}
			// Installing twice must not double-wrap: two hooks on the same
			// matcher both fire, and a command wrapped twice is a command sctx
			// runs against its own output.
			before, err := os.ReadFile(found.Path)
			if a.Wrapped() && err != nil {
				t.Fatalf("reading %s: %v", found.Path, err)
			}
			if _, errs := InstallWrapping(home, fakeBinary(t), agentdoc.SctxDoc); len(errs) > 0 {
				t.Fatalf("second install: %v", errs)
			}
			if a.Wrapped() {
				after, err := os.ReadFile(found.Path)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) {
					t.Errorf("a second install changed %s:\n--- before\n%s\n--- after\n%s", found.Path, before, after)
				}
			}
		})
	}
}

// mentionsAgent reports whether a section names this agent. Full name first,
// then any distinctive word of it: the documents call the client "Codex" where
// the table calls it "OpenAI Codex CLI", and a test that insists on one spelling
// is a test that fails on prose rather than on substance.
func mentionsAgent(section string, a agentdoc.Agent) bool {
	if strings.Contains(section, a.Name) {
		return true
	}
	for _, word := range strings.Fields(a.Name) {
		switch word {
		case "CLI", "Code", "OpenAI":
			continue
		}
		if strings.Contains(section, word) {
			return true
		}
	}
	return false
}

// A STEP ONLY A HUMAN CAN TAKE IS NEVER REPORTED AS DONE.
//
// Codex installs like every other hook and then does nothing until someone
// reviews and trusts it in the client. sctx cannot see that trust record, so the
// honest report is "installed, and waiting on you" — not [ok]. Reporting green
// here would repeat the exact failure this whole area exists to prevent: setup
// satisfied by what it wrote rather than by what the customer has.
func TestCodexAdmitsItsHookIsInertUntilTrusted(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errs := InstallWrapping(home, fakeBinary(t), agentdoc.SctxDoc); len(errs) > 0 {
		t.Fatal(errs)
	}
	states, err := InspectWrapping(home, fakeBinary(t), agentdoc.SctxDoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range states {
		if ws.AgentID != "codex" {
			continue
		}
		if !ws.OK {
			t.Fatalf("the hook was not installed: %s", ws.Detail)
		}
		if !ws.NeedsTrust {
			t.Error("Codex's hook is reported as active, but nothing has trusted it")
		}
		if !strings.Contains(ws.Detail, "/hooks") {
			t.Errorf("the report does not name the step that finishes it: %q", ws.Detail)
		}
		return
	}
	t.Fatal("codex was not detected")
}

// Every other client's wiring must NOT claim a pending human step, or the
// distinction stops meaning anything and people learn to ignore it.
func TestOnlyCodexClaimsAPendingTrustStep(t *testing.T) {
	for _, a := range agentdoc.KnownAgents {
		if !a.Wrapped() || a.ID == "codex" {
			continue
		}
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(a.Detect[0])), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, errs := InstallWrapping(home, fakeBinary(t), agentdoc.SctxDoc); len(errs) > 0 {
			t.Fatal(errs)
		}
		states, err := InspectWrapping(home, fakeBinary(t), agentdoc.SctxDoc)
		if err != nil {
			t.Fatal(err)
		}
		for _, ws := range states {
			if ws.AgentID == a.ID && ws.NeedsTrust {
				t.Errorf("%s reports a trust step it does not have", a.ID)
			}
		}
	}
}

// fakeBinary is a stand-in sctx that EXISTS on disk. Wiring is only healthy if
// the binary it names is still there, so a test using an imaginary path is
// testing the broken case by accident.
func fakeBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sctx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// WIRING THAT NAMES ANOTHER COPY OF SCTX IS WORKING WIRING.
//
// The plugin and the Codex hook embed the absolute path of whichever sctx
// installed them. Running a second copy — a dev build beside the Homebrew one —
// made setup report a perfectly functional install as [missing], and
// reinstalling only flipped the report for the other binary. What actually
// breaks wrapping is the named binary going away, and that is what must be
// reported.
func TestWiringIsJudgedByWhetherItsBinaryExists(t *testing.T) {
	for _, id := range []string{"kilocode", "codex"} {
		t.Run(id, func(t *testing.T) {
			a, _ := agentdoc.AgentByID(id)
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(a.Detect[0])), 0o755); err != nil {
				t.Fatal(err)
			}
			installer := fakeBinary(t)
			if _, errs := InstallWrapping(home, installer, agentdoc.SctxDoc); len(errs) > 0 {
				t.Fatal(errs)
			}

			// Inspected by a DIFFERENT sctx that also exists: still healthy, and
			// no reinstall is needed to make it so.
			other := fakeBinary(t)
			states, err := InspectWrapping(home, other, agentdoc.SctxDoc)
			if err != nil {
				t.Fatal(err)
			}
			for _, ws := range states {
				if ws.AgentID == id && !ws.OK {
					t.Errorf("wiring installed by another copy of sctx was called broken: %s", ws.Detail)
				}
			}

			// The binary it names goes away: now it really is broken, and the
			// report has to name the path so the remedy is obvious.
			if err := os.Remove(installer); err != nil {
				t.Fatal(err)
			}
			states, err = InspectWrapping(home, other, agentdoc.SctxDoc)
			if err != nil {
				t.Fatal(err)
			}
			for _, ws := range states {
				if ws.AgentID != id {
					continue
				}
				if ws.OK {
					t.Error("wiring calling a binary that no longer exists was reported as working")
				}
				if !strings.Contains(ws.Detail, installer) {
					t.Errorf("the missing binary is not named: %q", ws.Detail)
				}
			}
		})
	}
}
