package agentdoc

// These cover the shipped DOCUMENTS and the agent TABLE — the definitions this
// package owns. The install behaviour that consumes them is tested next to the
// code that touches a disk, in internal/platform/agentsetup.

import (
	"strings"
	"testing"
)

// Detection must key on the agent's OWN configuration, never on anything we
// wrote. Keyed on our own output, an agent would look configured forever after
// one accidental --agent run.
func TestDetectionNeverKeysOnOurOwnFiles(t *testing.T) {
	for _, a := range KnownAgents {
		for _, d := range a.Detect {
			for _, ours := range []string{"SCTX.md", "SYNAPCTX.md", "synapctx.md"} {
				if strings.HasSuffix(d, ours) {
					t.Errorf("agent %q detects on %q, which is a file we write", a.ID, d)
				}
			}
		}
	}
}

// The templates load into every session's context window. A token-optimizing
// product that ships a bloated instruction file is arguing against itself.
func TestTemplatesStaySmall(t *testing.T) {
	for _, d := range []Doc{SctxDoc, SynapctxDoc} {
		body := d.Body([]string{"acme"})
		if n := len(body); n > 4000 {
			t.Errorf("%s is %d bytes; it is loaded on every turn, keep it under 4000", d.Name, n)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s is empty", d.Name)
		}
	}
}

// Mandate-shaped wording is refused on purpose: it works, and it makes usage
// unfalsifiable, so we could never tell whether a customer's agent chose the
// tool on merit. Trigger-shaped wording was measured to work without that cost.
func TestSynapctxTemplateIsTriggerShapedNotMandateShaped(t *testing.T) {
	body := synapctxBody([]string{"acme"})
	for _, banned := range []string{"USE IT FIRST", "always call", "ALWAYS call", "MUST go through", "before reaching for Grep"} {
		if strings.Contains(body, banned) {
			t.Errorf("template contains mandate-shaped wording %q", banned)
		}
	}
	if !strings.Contains(body, "**Before ") && !strings.Contains(body, "**When ") {
		t.Error("template has no trigger-shaped guidance at all; that measured zero calls across five tools")
	}
}

// Passing the wrong organization returns a confidently wrong answer rather than
// an error, so a multi-org machine has to be told to always pass it.
func TestMultiOrgTemplateDemandsExplicitScoping(t *testing.T) {
	single := synapctxBody([]string{"acme"})
	multi := synapctxBody([]string{"acme", "globex"})
	if strings.Contains(single, "Always pass") && !strings.Contains(single, "you can omit") {
		t.Error("a single-org machine should not be nagged about scoping")
	}
	if !strings.Contains(multi, "globex") || !strings.Contains(multi, "Always pass") {
		t.Errorf("multi-org template must name every org and demand explicit scoping:\n%s", multi)
	}
}

// The instruction file's job is no longer just "these tools exist" — it is
// "here is how to check whether an answer can be trusted". An agent that does
// not know a warrant is attached will not read it, and an unread limitation is
// no limitation at all.
func TestSynapctxTemplateTeachesTheWarrant(t *testing.T) {
	body := synapctxBody([]string{"acme"})
	for _, want := range []string{
		"answer warrant",
		"Compare it against your checkout",
		"ranked, not exhaustive",
		"DEGRADED",
		"truncated",
		"language_unsupported",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("template does not teach %q; the warrant ships unread", want)
		}
	}
	// The distinction the whole trust argument rests on.
	if !strings.Contains(body, `"no callers" means a change is safe`) {
		t.Error("template must explain why not-analysed differs from no-callers")
	}
}
