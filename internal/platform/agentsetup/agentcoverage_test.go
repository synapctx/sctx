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
			if _, errs := InstallWrapping(home, "/usr/local/bin/sctx", agentdoc.SctxDoc); len(errs) > 0 {
				t.Fatalf("install: %v", errs)
			}
			states, err := InspectWrapping(home, "/usr/local/bin/sctx", agentdoc.SctxDoc)
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
			if _, errs := InstallWrapping(home, "/usr/local/bin/sctx", agentdoc.SctxDoc); len(errs) > 0 {
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
