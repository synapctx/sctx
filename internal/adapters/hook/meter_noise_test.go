package hook

import "testing"

// Both defects were found by looking at the LIVE meter rather than at the code:
// a ranked list of what customers run is also a list of what we are recording
// wrongly.

// `for` appeared 19 times. `for f in ...; do` is a loop, not a program, and a
// formatter for it is not a thing — but it competes with real gaps for the
// attention that decides what gets built next.
func TestShellKeywordsAreNotCoverageGaps(t *testing.T) {
	for _, cmd := range []string{
		"for f in *.go; do echo $f; done",
		"if test -f x; then echo y; fi",
		"while read l; do echo $l; done",
	} {
		if seg, ok := gapSegment(cmd); ok {
			t.Errorf("gapSegment(%q) recorded %q as a gap; a shell keyword is not a program", cmd, seg)
		}
	}
}

// `./bin/sctx` appeared as a gap: a developer running a locally-built sctx was
// reported as a command sctx cannot handle, which is the opposite of the truth.
// The already-wrapped guard compared the raw token, so any path-qualified
// invocation walked past it.
func TestAPathQualifiedSctxIsRecognisedAsAlreadyWrapped(t *testing.T) {
	for _, cmd := range []string{
		"./bin/sctx go test ./...",
		"/usr/local/bin/sctx docker ps",
		"sctx go test ./...",
	} {
		if seg, ok := gapSegment(cmd); ok {
			t.Errorf("gapSegment(%q) recorded %q; this command is already wrapped", cmd, seg)
		}
	}
}

// The counterweight: a real gap must still be recorded, or the fixes above have
// quietly turned the meter off.
func TestRealGapsSurviveTheNoiseFilters(t *testing.T) {
	for _, cmd := range []string{"mix test", "cd sub && mix test", "mix test | tail -5"} {
		if _, ok := gapSegment(cmd); !ok {
			t.Errorf("gapSegment(%q) recorded nothing; mix test is a genuine gap", cmd)
		}
	}
}

// KNOWN LIMITATION, pinned deliberately rather than fixed. A command inside a
// loop BODY is not recorded: the segment is `do mix test`, whose head is the
// keyword, and gapSegment skips a noise-headed segment rather than looking past
// the keyword within it.
//
// Left alone because the fix belongs in the segment scanner, which is the most
// delicate code in this package (quote-aware, heredoc-aware, fuzz-guarded), and
// the trade is asymmetric: under-counting loses signal we can live without,
// while over-counting `for` sends us building a formatter for a shell keyword.
//
// If loop bodies ever matter, change this test first and say why.
func TestACommandInsideALoopBodyIsNotRecorded(t *testing.T) {
	if seg, ok := gapSegment("for f in x; do mix test; done"); ok {
		t.Errorf("gapSegment recorded %q from a loop body — behaviour changed; is that intended?", seg)
	}
}
