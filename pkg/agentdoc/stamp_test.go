package agentdoc

import (
	"strings"
	"testing"
)

// The round trip that makes every other guarantee possible. If a written body
// cannot be read back as ours-and-untouched, the installer falls back to never
// updating anything and the whole mechanism is inert.
func TestAWrittenSidecarReadsBackAsCurrent(t *testing.T) {
	for _, d := range []Doc{SctxDoc, SynapctxDoc} {
		for _, orgs := range [][]string{nil, {"acme"}, {"acme", "globex"}} {
			body := d.Body(orgs)
			file := StampedBody(body)

			hash, rest, ok := ParseStamp(file)
			if !ok {
				t.Fatalf("%s: a file we just wrote is not recognised as stamped", d.Name)
			}
			if rest != body {
				t.Errorf("%s: body did not survive the round trip", d.Name)
			}
			if hash != bodyHash(body) {
				t.Errorf("%s: stamp does not match the body it was written for", d.Name)
			}
			if got := ClassifySidecar(file, body); got != SidecarCurrent {
				t.Errorf("%s with %d org(s): state = %v, want current", d.Name, len(orgs), got)
			}
		}
	}
}

// The four states, each of which authorises a different action. Getting one wrong
// either overwrites a developer's work or strands a correctness fix forever.
func TestSidecarStatesAuthoriseTheRightAction(t *testing.T) {
	body := "# Doc\n\nsome guidance\n"
	stamped := StampedBody(body)

	if got := ClassifySidecar("", body); got != SidecarMissing {
		t.Errorf("absent file: %v, want missing", got)
	}
	if got := ClassifySidecar(stamped, body); got != SidecarCurrent {
		t.Errorf("freshly written: %v, want current", got)
	}
	// Ours, untouched, template moved on — the case the whole mechanism exists
	// for, and the only one a plain --install may overwrite.
	if got := ClassifySidecar(stamped, body+"a newly added caveat\n"); got != SidecarStale {
		t.Errorf("template moved on: %v, want stale", got)
	}
	// Edited: the stamp no longer describes the body beneath it.
	if got := ClassifySidecar(stamped+"my own note\n", body); got != SidecarEdited {
		t.Errorf("hand-edited: %v, want edited", got)
	}
	// Pre-stamp install, or a paste from an older page.
	if got := ClassifySidecar(body, body); got != SidecarUnverifiable {
		t.Errorf("unstamped: %v, want unverifiable", got)
	}
	// A one-line file cannot be stamped, and must not panic or be read as ours.
	if got := ClassifySidecar("no newline at all", body); got != SidecarUnverifiable {
		t.Errorf("single line: %v, want unverifiable", got)
	}
}

// An edit that happens to move the file TOWARD the current template is still an
// edit. Classifying it as stale would let a plain install silently discard
// whatever else the developer changed in the same pass.
func TestAnEditTowardTheTemplateIsStillAnEdit(t *testing.T) {
	body := "# Doc\n\nsome guidance\n"
	// Stamped for an older body, but its content now equals what we would write.
	forged := Stamp("an older body\n") + "\n" + body
	if got := ClassifySidecar(forged, body); got != SidecarEdited {
		t.Errorf("state = %v, want edited: the stamp does not describe this body", got)
	}
}

// The stamp is a comment in every document we write, so it must not disturb the
// markdown around it or be mistaken for content.
func TestTheStampIsAnInertComment(t *testing.T) {
	file := StampedBody(SctxDoc.Body(nil))
	first, _, _ := strings.Cut(file, "\n")
	if !strings.HasPrefix(first, "<!--") || !strings.HasSuffix(first, "-->") {
		t.Errorf("stamp is not an HTML comment: %q", first)
	}
	if len(first) > 40 {
		t.Errorf("stamp is %d bytes; it is loaded every session, keep it minimal: %q", len(first), first)
	}
}
