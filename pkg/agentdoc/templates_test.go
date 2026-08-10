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
//
// MEASURED ON THE MULTI-ORG VARIANT, which is the longest one and the one a
// multi-org machine actually loads. Measuring only the single-org body let the
// scope block grow unwatched — the variant under the ceiling was not the variant
// customers were served.
//
// IN TOKENS, NOT BYTES. This guard and the proxy's schemaCostCeiling protect the
// same property — what every session pays before any work happens — and used to
// be written as "4000" in both while meaning 4,000 BYTES here and 4,000 TOKENS
// there. Two guards that read as equal and differ 4x cannot be reasoned about
// together, and the number that actually matters is their SUM. Same unit now, so
// they add up; the combined ceiling lives in the proxy, which can import this.
//
// bytes/4 is sctx's own token convention, used here because this module must
// stay dependency-free and so cannot import the real estimator.
//
// It UNDER-counts prose by roughly 13%: measured 2026-08-10, tokenest reads
// SCTX.md as ~1,090 tokens where bytes/4 reads ~953. So this ceiling is looser
// than its number suggests — treat it as an early-warning line, not the
// authority. The authoritative figure is the COMBINED always-on cost asserted in
// developer-mcp-proxy (internal/adapters/api/mcp/alwaysoncost_test.go), which can
// import the real estimator and also sees the tool descriptions this file must
// not duplicate.
const docTokenCeiling = 1000

func estimatedTokens(body string) int { return len(body) / 4 }

func TestTemplatesStaySmall(t *testing.T) {
	for _, d := range []Doc{SctxDoc, SynapctxDoc} {
		for _, orgs := range [][]string{{"acme"}, {"acme", "globex", "initech"}} {
			body := d.Body(orgs)
			// Logged, not merely asserted: the proxy's schema-cost test prints its
			// number for the same reason — a guard that only speaks when it fails
			// leaves the trend invisible until the day it breaks.
			t.Logf("%-12s %d org(s): ~%d tokens (%d bytes)", d.Name, len(orgs), estimatedTokens(body), len(body))
			if n := estimatedTokens(body); n > docTokenCeiling {
				t.Errorf("%s is ~%d tokens (%d bytes) for %d org(s); it is loaded on every\n"+
					"turn, keep it under %d — or move the content to the vehicle that already\n"+
					"carries it (see the placement rule in templates.go)",
					d.Name, n, len(body), len(orgs), docTokenCeiling)
			}
			if strings.TrimSpace(body) == "" {
				t.Errorf("%s is empty", d.Name)
			}
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

// A multi-org machine holds one API KEY PER ORGANIZATION, and the key — not the
// `organization` argument — decides which one answers. So the template must send
// the agent to the right CREDENTIAL, not merely tell it to name an org.
//
// This test used to assert the opposite ("must demand explicit scoping"), on the
// premise that a wrong organization returned a confidently wrong answer rather
// than an error. Per-key auth made that false in both halves: omitting now means
// the KEY'S OWN organization rather than a process-wide default, and naming one
// the key does not cover is refused outright. The old guidance therefore produced
// a guaranteed refusal on every cross-org call, which reads as "that org is
// unknown or inactive" and was repeatedly mistaken for a broken index.
func TestMultiOrgTemplateRoutesByCredential(t *testing.T) {
	single := synapctxBody([]string{"acme"})
	multi := synapctxBody([]string{"acme", "globex"})

	if strings.Contains(single, "Always pass") {
		t.Error("a single-org machine should not be nagged about scoping")
	}
	if !strings.Contains(multi, "globex") {
		t.Errorf("multi-org template must name every configured org:\n%s", multi)
	}
	// The instruction that actually prevents the failure.
	if !strings.Contains(multi, "its own API key") || !strings.Contains(multi, "OMIT") {
		t.Errorf("multi-org template must route by credential and say to omit the\n"+
			"organization argument, or every cross-org call is refused:\n%s", multi)
	}
	// And it must pre-empt the misreading, because the refusal cannot say more:
	// its wording is deliberately identical to a genuinely unknown organization.
	//
	// Compared with whitespace collapsed: the phrase is prose that will be
	// re-wrapped, and a guard that breaks on a line break would fail for a reason
	// having nothing to do with what it is protecting.
	flat := strings.Join(strings.Fields(multi), " ")
	if !strings.Contains(flat, "unknown or inactive") || !strings.Contains(flat, `"wrong key"`) {
		t.Errorf("multi-org template must teach that the refusal means the wrong key\n"+
			"was used, not that the index is broken:\n%s", multi)
	}
	// The old mandate must not creep back: with per-org keys it is the bug.
	if strings.Contains(multi, "Always pass") {
		t.Errorf("multi-org template tells the agent to always pass `organization`,\n"+
			"which with one key per organization is refused, not scoped:\n%s", multi)
	}
}

// The file must teach that answers state their own limits and that those lines
// are to be READ — an agent that does not expect a caveat will skim past one.
//
// It must NOT re-teach the vocabulary. This test used to require six specific
// warrant keywords (`DEGRADED`, `truncated`, `language_unsupported`, …), which
// forced the file to define terms the ANSWER already defines in full sentences
// carrying their own next action (`formatWarrant`, `trackedRefsNotice` and
// friends in the proxy's mcp adapter). Every one of those definitions was
// therefore paid for twice per session, in the vehicle that decays rather than
// the one attached to the result being qualified.
func TestSynapctxTemplateTeachesAnswersCarryTheirOwnLimits(t *testing.T) {
	body := synapctxBody([]string{"acme"})
	flat := strings.Join(strings.Fields(body), " ")

	if !strings.Contains(flat, "Answers carry their own limits") {
		t.Error("template does not tell the agent answers state their own limits; an\n" +
			"unexpected caveat is a skimmed caveat")
	}
	if !strings.Contains(flat, "read them") {
		t.Error("template does not instruct the agent to READ those lines")
	}
	// The one inference the file must block, because making it is what turns a
	// ranked answer into a wrong deletion.
	if !strings.Contains(flat, `licenses "it does not exist"`) {
		t.Error("template must deny that an absence in a ranked answer proves absence")
	}
}

// The dedup guard, on this side of the wire. Each of these is rendered ON an
// answer or carried by a tool's own description, so re-stating it here bills the
// customer twice for one sentence. The proxy holds the general version of this
// guard (a shingle comparison against every registered description); these are
// the specific regressions worth naming, since they are the ones that were
// actually present and the ones a well-meaning author would re-add.
func TestTemplateDoesNotRestateWhatAnswersAlreadyCarry(t *testing.T) {
	for _, orgs := range [][]string{nil, {"acme"}, {"acme", "globex"}} {
		body := synapctxBody(orgs)
		for _, banned := range []string{
			"language_unsupported", // find_references says this on the answer
			"include_fields",       // retrieve_context's own description
			"ranked, not exhaustive",
			"DEGRADED",
			"tracked refs NOT in this answer",
			"COMPLETE set",
			"budget-exhausted",
			"matched semantically",
			"reached by graph traversal", // the related/lexical taxonomy
		} {
			if strings.Contains(body, banned) {
				t.Errorf("template restates %q, which already rides the answer or the tool\n"+
					"description — the customer pays for it twice every session.\n"+
					"See the placement rule in templates.go.", banned)
			}
		}
	}
}
