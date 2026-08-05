package hook

import (
	"strings"
	"testing"
)

// This runs on EVERY Bash command an agent issues, so the rejections matter more
// than the acceptances: a false positive costs a pointless round trip and an
// irrelevant line in the agent's context, while a false negative costs nothing
// but a missed opportunity. Every case below is a command shape that actually
// appears in this repository's own telemetry.
func TestGrepSymbolAcceptsOnlyRealSymbolSearches(t *testing.T) {
	for _, tc := range []struct {
		cmd, want, why string
	}{
		// Accepted.
		{`grep -rn PaymentService ./internal`, "PaymentService", "the ordinary case"},
		{`rg findReferences`, "findReferences", "rg counts too"},
		{`/usr/bin/grep -r resolveTenant .`, "resolveTenant", "an absolute program path"},
		{`grep -rn "retrieveContext" .`, "retrieveContext", "quotes are stripped"},
		{`grep -rn progkey.Key ./app`, "progkey.Key", "a qualified name is MORE specific, not less"},

		// Rejected — and each for a different reason.
		{`grep -rn "func Foo" .`, "", "a phrase is a text search, not a symbol"},
		{`grep -rn 'TODO|FIXME' .`, "", "a regex alternation"},
		{`grep -rn err .`, "", "too short: matches everywhere, the answer is never actionable"},
		{`grep -rn 42 .`, "", "a magic number, not a symbol"},
		{`grep -rn ^package .`, "", "an anchor is a regex"},
		{`grep -rn foo/bar .`, "", "a path fragment"},
		{`go test ./...`, "", "not a search at all"},
		{`ls -la`, "", "not a search at all"},
		{``, "", "empty"},
		{`grep`, "", "no pattern"},

		// The configuration we actually ship: the Bash hook has already
		// rewritten `grep` to `sctx grep`, so failing to see through the wrapper
		// would mean this feature never fires for any real user.
		{`sctx grep -rn PaymentService ./internal`, "PaymentService", "sees through our own rewrite"},
		{`/Users/x/.local/bin/sctx grep -rn resolveTenant .`, "resolveTenant", "…with a path too"},
		{`sctx grep -rn resolveTenant .`, "resolveTenant", "and the sctx wrapper"},
	} {
		if got := grepSymbol(tc.cmd); got != tc.want {
			t.Errorf("grepSymbol(%q) = %q, want %q — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

// A quoted phrase must arrive as ONE argument. Split naively, `grep "func Foo"`
// yields `func` — a plausible-looking identifier that would fire the nudge on
// every phrase search in the repository.
func TestSplitArgsKeepsQuotedPhrasesWhole(t *testing.T) {
	got := splitArgs(`grep -rn "func Foo" ./app`)
	want := []string{"grep", "-rn", "func Foo", "./app"}
	if len(got) != len(want) {
		t.Fatalf("splitArgs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsPlainIdentifierBoundaries(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"abcde", true},   // exactly the minimum
		{"abcd", false},   // one under
		{"a_b_c_d", true}, // underscores are identifier characters
		{"pkg.Sym", true},
		{"12345", false}, // digits only
		{"abc de", false},
		{"abc-de", false}, // a hyphen is a regex range or a flag
		{"", false},
	} {
		if got := isPlainIdentifier(tc.in); got != tc.want {
			t.Errorf("isPlainIdentifier(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The message must state what the grep could NOT see and name the tool that
// answers it. Repeating the local matches would be telling the developer what is
// already on their screen, and a suggestion with no next step is an advert.
func TestSymbolContextSaysOnlyWhatGrepCouldNotSee(t *testing.T) {
	got := symbolContext("PaymentService", elsewhereResult{
		Elsewhere: 4, Repositories: []string{"acme/jobs", "acme/web"},
	})
	for _, want := range []string{"4 call site", "acme/jobs, acme/web", "find_references", "this checkout"} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q:\n%s", want, got)
		}
	}
	if got := symbolContext("X", elsewhereResult{}); got != "" {
		t.Errorf("spoke when there was nothing grep had missed: %q", got)
	}
}
