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
	for _, version := range []string{"", "v0.4.4", "v0.4.4-rc-1", "v0.4.3-2-gabc1234-dirty"} {
		file := StampedBodyFor(version, SctxDoc.Body(nil))
		first, _, _ := strings.Cut(file, "\n")
		if !strings.HasPrefix(first, "<!--") || !strings.HasSuffix(first, "-->") {
			t.Errorf("stamp is not an HTML comment: %q", first)
		}
		// One short comment line per document per session. The budget is here
		// because this text is loaded on every turn of every session, and a
		// development build's `git describe` version is the longest thing that
		// can land in it.
		if len(first) > 64 {
			t.Errorf("stamp is %d bytes; it is loaded every session, keep it minimal: %q", len(first), first)
		}
	}
}

// BACKWARD COMPATIBILITY IS THE POINT OF PUTTING THE HASH LAST. Every document
// installed before versions were recorded — and every one synapctx.com serves
// today, which renders these bytes without knowing which sctx it corresponds to
// — carries a version-less stamp. If those stopped reading as ours, a plain
// `--install` would refuse to update them and the fix this release ships would
// reach nobody who already installed.
func TestAVersionlessStampIsStillFullyValid(t *testing.T) {
	body := SctxDoc.Body(nil)
	legacy := "<!-- sctx-doc " + bodyHash(body) + " -->\n" + body

	if got := StampedBody(body); got != legacy {
		t.Errorf("StampedBody no longer writes the version-less form:\ngot  %q\nwant %q", got, legacy)
	}
	info, rest, ok := ParseStampInfo(legacy)
	if !ok || rest != body {
		t.Fatalf("a version-less stamp did not parse: ok=%t", ok)
	}
	if info.Version != "" {
		t.Errorf("invented a version %q for a stamp that has none", info.Version)
	}
	if got := ClassifySidecar(legacy, body); got != SidecarCurrent {
		t.Errorf("state = %v, want current", got)
	}
}

// The version is provenance for a human to read. The HASH decides, and these
// are the cases where trusting the version instead would be wrong: a downgrade
// installs an older document that is nonetheless exactly what that build ships,
// and a `dev` build's version never changes while its body does.
func TestTheVersionIsRecordedButTheHashDecides(t *testing.T) {
	body := "# Doc\n\nsome guidance\n"
	stamped := StampedBodyFor("v0.4.4", body)

	info, rest, ok := ParseStampInfo(stamped)
	if !ok || rest != body {
		t.Fatalf("a versioned stamp did not round-trip: ok=%t", ok)
	}
	if info.Version != "v0.4.4" || info.Hash != bodyHash(body) {
		t.Errorf("parsed %+v, want version v0.4.4 and the body hash", info)
	}
	// Same body, older recorded version: current, not stale. Nothing to do.
	if got := ClassifySidecar(StampedBodyFor("v0.1.0", body), body); got != SidecarCurrent {
		t.Errorf("older version, identical body: %v, want current", got)
	}
	// Same recorded version, moved-on body: stale, and a plain install updates it.
	if got := ClassifySidecar(stamped, body+"a new caveat\n"); got != SidecarStale {
		t.Errorf("same version, changed template: %v, want stale", got)
	}
	// ParseStamp keeps its old shape for callers that only ever wanted the hash.
	if hash, _, ok := ParseStamp(stamped); !ok || hash != bodyHash(body) {
		t.Errorf("ParseStamp no longer recovers the hash from a versioned stamp")
	}
}

// A version we cannot render as one field would produce a stamp that parses
// back differently from what was written. Degrade to the version-less form
// rather than write something the parser has to guess at.
func TestAnUnusableVersionDegradesToTheVersionlessStamp(t *testing.T) {
	body := "# Doc\n\ntext\n"
	for _, version := range []string{"", "   ", "v1 with spaces", "v2\nv3", "v4 -->"} {
		if got, want := StampFor(version, body), Stamp(body); got != want {
			t.Errorf("version %q produced %q, want the version-less stamp %q", version, got, want)
		}
	}
}
