package agentdoc

import (
	"strings"
	"testing"
)

// The manual path and the automatic path must converge, not compound.
//
// synapctx.com/sctx/ publishes "add this to your instruction file" for people who
// have not installed the binary. If what it shows is not what the installer reads
// back as its own, the next `sctx setup --install` appends a SECOND copy of the
// same document — and both then load into every session, forever. Wrap and BlockOf
// are the two ends of that contract, so a round trip is the guard.
func TestWhatWeTellPeopleToPasteIsWhatWeReadBack(t *testing.T) {
	for _, a := range KnownAgents {
		body := BlockBody(a, []string{"acme"}, []Doc{SctxDoc}, "")
		got, ok := BlockOf(Wrap(body))
		if !ok {
			t.Errorf("%s: a wrapped block is not recognised as installed", a.ID)
			continue
		}
		if strings.TrimSpace(got) != strings.TrimSpace(body) {
			t.Errorf("%s: round trip altered the block:\nwant %q\ngot  %q", a.ID, body, got)
		}
	}
}

// Every published row has to be actionable. A blank path on a public page is
// worse than an omitted agent: it reads as guidance and cannot be followed.
func TestEveryAgentIsPublishable(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range KnownAgents {
		if a.ID == "" || a.Name == "" || a.Root == "" || len(a.Detect) == 0 {
			t.Errorf("agent %+v has an empty field; it cannot be published or detected", a)
		}
		if seen[a.ID] {
			t.Errorf("duplicate agent id %q — ?agent= would be ambiguous", a.ID)
		}
		seen[a.ID] = true
		// Home-relative, always. An absolute or escaping path would be rendered
		// against the reader's home directory on the website and point somewhere
		// nobody intended.
		if strings.HasPrefix(a.Root, "/") || strings.HasPrefix(a.Root, "~") || strings.Contains(a.Root, "..") {
			t.Errorf("agent %q root %q is not home-relative", a.ID, a.Root)
		}
	}
}

// Include support is opt-IN, and this is the guard on that.
//
// Whether a given tool resolves `@file.md` is a fact about THAT TOOL, so no test
// in this repository can check the flag is right — comparing the flag against the
// block it produces is a tautology, because the block is derived from the flag.
// What can be pinned is the conservative default: marking an agent include-capable
// is a claim someone verified against a real version, so it takes a deliberate edit
// HERE as well as there. Inline works everywhere; a wrong `@SCTX.md` is a line that
// silently loads nothing, and on the website it is published guidance that does
// nothing.
//
// Add an id below only alongside the version you verified it against.
func TestIncludeSupportStaysOptIn(t *testing.T) {
	verified := map[string]string{
		"claude": "Claude Code resolves @file imports in CLAUDE.md — verified in use",
	}
	for _, a := range KnownAgents {
		why, ok := verified[a.ID]
		if a.Includes && !ok {
			t.Errorf("%s is marked include-capable but is not in the verified set; "+
				"inline is correct until someone checks a real version", a.ID)
		}
		if !a.Includes && ok {
			t.Errorf("%s is verified include-capable (%s) but the flag is off, so its "+
				"whole document is inlined into the developer's file", a.ID, why)
		}
	}
}

// The two block shapes, documented by example: a reference for include-capable
// agents, the text itself for everyone else. This pins the RENDERING, not the
// flag — see TestIncludeSupportStaysOptIn for why those are different claims.
func TestTheBlockCarriesAReferenceOrTheTextAccordingly(t *testing.T) {
	for _, a := range KnownAgents {
		body := BlockBody(a, nil, []Doc{SctxDoc}, "")
		hasRef := strings.Contains(body, "@"+SctxDoc.Name)
		hasText := strings.Contains(body, "# sctx — token-optimized command output")
		if a.Includes && (!hasRef || hasText) {
			t.Errorf("%s: include-capable block should be a bare reference, got %q", a.ID, body)
		}
		if !a.Includes && (!hasText || hasRef) {
			t.Errorf("%s: non-including block must carry the text itself, got %q", a.ID, body)
		}
	}
}
