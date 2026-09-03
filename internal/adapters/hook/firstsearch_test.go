package hook

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
)

func runFirstSearch(t *testing.T, cfg config.Config, cwd, session, tool, pattern string) string {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(fmt.Sprintf(
		`{"session_id":%q,"cwd":%q,"tool_name":%q,"tool_input":{"pattern":%q}}`,
		session, cwd, tool, pattern))
	if code := RunClaudeFirstSearch(in, &out, cfg); code != 0 {
		t.Fatalf("exit code = %d, want 0; a PreToolUse hook that fails can block a search", code)
	}
	return out.String()
}

// Two nudges, then silence. An agent that ignored the same sentence twice will
// ignore it a third time, and from there it is a per-turn cost with no upside.
func TestFirstSearchSpeaksTwiceThenStops(t *testing.T) {
	const token = "tok-abc"
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")
	cfg := cfgFor(nil, token)

	first := runFirstSearch(t, cfg, root, "sess-1", "Glob", "**/*.go")
	if !strings.Contains(first, "local search #1 this session, in org acme") {
		t.Errorf("first search was not counted as #1:\n%s", first)
	}
	// The envelope matters as much as the text: PreToolUse reads
	// additionalContext, and the event name must be PreToolUse or the host
	// discards the whole object without a word.
	flat := strings.ReplaceAll(first, " ", "")
	for _, want := range []string{`"hookEventName":"PreToolUse"`, `"additionalContext"`} {
		if !strings.Contains(flat, strings.ReplaceAll(want, " ", "")) {
			t.Errorf("envelope is missing %s:\n%s", want, first)
		}
	}
	// And it must NOT carry a permission decision. `"allow"` is an affirmative
	// auto-approval: on the Agent matcher it would silently suppress the
	// developer's own `ask` rule for the first two spawns of every session. A
	// hook that exists to add a sentence must never widen a permission.
	if strings.Contains(flat, `"permissionDecision"`) {
		t.Errorf("nudge returned a permission decision; it must defer to the normal flow:\n%s", first)
	}
	if !strings.Contains(first, "mcp__acme-graph__retrieve_context") ||
		!strings.Contains(first, "mcp__acme-graph__recall_memory") {
		t.Errorf("nudge does not name the tools in this machine's namespace:\n%s", first)
	}

	second := runFirstSearch(t, cfg, root, "sess-1", "Glob", "**/*.go")
	if !strings.Contains(second, "local search #2") {
		t.Errorf("the counter did not survive across calls:\n%s", second)
	}

	if third := runFirstSearch(t, cfg, root, "sess-1", "Glob", "**/*.go"); third != "" {
		t.Errorf("the third search still produced a nudge:\n%s", third)
	}

	// The counter is PER SESSION: a new session starts over, because its agent
	// has not seen the sentence.
	if fresh := runFirstSearch(t, cfg, root, "sess-2", "Glob", "**/*.go"); !strings.Contains(fresh, "local search #1") {
		t.Errorf("a new session did not start its own count:\n%s", fresh)
	}
}

// find_references is offered only for something that is actually a SYMBOL,
// reusing the post-tool nudge's precision gate. Offering to list call sites for
// a glob or a phrase is an advert.
func TestFirstSearchOffersFindReferencesOnlyForASymbolPattern(t *testing.T) {
	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")
	cfg := cfgFor(nil, token)
	const sentence = "find_references lists every call site across repositories"

	t.Run("grep for a plain identifier", func(t *testing.T) {
		t.Setenv("SCT__SPOOL_DIR", t.TempDir())
		got := runFirstSearch(t, cfg, root, "sess-sym", "Grep", "ProcessInvoice")
		if !strings.Contains(got, "Before changing ProcessInvoice, "+sentence) {
			t.Errorf("no find_references offer for a symbol search:\n%s", got)
		}
	})
	t.Run("grep for a regex is not a symbol", func(t *testing.T) {
		t.Setenv("SCT__SPOOL_DIR", t.TempDir())
		got := runFirstSearch(t, cfg, root, "sess-re", "Grep", "func .*Invoice\\(")
		if strings.Contains(got, sentence) {
			t.Errorf("offered find_references for a regex:\n%s", got)
		}
	})
	t.Run("glob never offers it", func(t *testing.T) {
		t.Setenv("SCT__SPOOL_DIR", t.TempDir())
		// Same string that qualifies as a symbol above: it is the TOOL, not the
		// pattern, that makes this meaningless — a Glob pattern is a path.
		got := runFirstSearch(t, cfg, root, "sess-glob", "Glob", "ProcessInvoice")
		if strings.Contains(got, sentence) {
			t.Errorf("offered find_references for a Glob:\n%s", got)
		}
	})
}

// No key for this org means there is nothing to point the agent at.
func TestFirstSearchIsSilentWithoutAKeyForTheOrg(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	fakeClaudeConfig(t, "tok-abc", "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")

	cfg := config.Config{TelemetryEndpoint: "http://unused/v1/events", OrgTokens: map[string]string{"other": "tok"}}
	if got := runFirstSearch(t, cfg, root, "sess-1", "Grep", "ProcessInvoice"); got != "" {
		t.Errorf("nudged without a key for this org:\n%s", got)
	}
}

// Outside a repository there is no org to name and no graph to point at.
func TestFirstSearchIsSilentOutsideARepository(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	fakeClaudeConfig(t, "tok-abc", "acme-graph")
	if got := runFirstSearch(t, cfgFor(nil, "tok-abc"), t.TempDir(), "sess-1", "Grep", "ProcessInvoice"); got != "" {
		t.Errorf("nudged outside a repository:\n%s", got)
	}
}

// The session id is client-supplied and is interpolated into a PATH. Anything
// that could escape the counter directory is dropped rather than escaped.
func TestSessionIDIsSanitisedBeforeBecomingAFilename(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"abc-123.def_G", "abc-123.def_G"},
		{"../../etc/passwd", "....etcpasswd"},
		{"a/b", "ab"},
		{"", ""},
		{"..", ""},   // would still address the parent directory
		{"/", ""},    // nothing survives
		{"\x00", ""}, // a NUL truncates a path in the syscall layer
	} {
		if got := sanitizeSessionID(tc.in); got != tc.want {
			t.Errorf("sanitizeSessionID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
