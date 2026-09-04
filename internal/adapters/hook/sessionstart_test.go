package hook

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
	"github.com/synapctx/sctx/internal/platform/httpclient"
)

// fakeRepo builds a checkout gitrepo.Detect reads as "acme/widgets", with a real
// enough .git that localHead can resolve HEAD without shelling out to git.
func fakeRepo(t *testing.T, sha string) string {
	t.Helper()
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(git, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAt := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(git, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeAt("config", "[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n")
	writeAt("HEAD", "ref: refs/heads/main\n")
	writeAt(filepath.Join("refs", "heads", "main"), sha+"\n")
	return root
}

// fakeClaudeConfig registers an MCP server under HOME so the brief can name the
// real tool namespace rather than guessing from the org slug.
func fakeClaudeConfig(t *testing.T, token, server string) string {
	t.Helper()
	home := t.TempDir()
	body := fmt.Sprintf(`{"mcpServers":{%q:{"headers":{"Authorization":"Bearer %s"}}}}`, server, token)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

const briefFixture = `{
  "organization": "acme",
  "repository": {
    "name": "acme/widgets",
    "indexingStatus": "ready",
    "refs": [
      {"name": "main", "role": "primary", "indexedCommitSha": "abcdef0123456789", "indexedAt": "2026-09-01T10:00:00Z"},
      {"name": "develop", "role": "secondary", "indexedCommitSha": "999", "indexedAt": "2026-09-02T10:00:00Z"}
    ]
  },
  "retrievalHint": "retrieve_context {query: \"acme/widgets entry points and service boundaries\", repository: \"acme/widgets\"}",
  "notes": [
    {"id": "n1", "kind": "decision", "createdAt": "2026-08-20T09:00:00Z",
     "text": "Widget ids are lowercase ULIDs; the uppercase constraint was dropped after every insert failed.",
     "repositories": ["acme/widgets"]}
  ]
}`

// briefServer asserts the bearer on the way through: a brief fetched without the
// org's own key would be either a leak or an error, never a useful answer.
func briefServer(t *testing.T, token, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want bearer %q", got, token)
		}
		if r.URL.Path != sessionStartPath {
			t.Errorf("path = %q, want %q", r.URL.Path, sessionStartPath)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cfgFor(srv *httptest.Server, token string) config.Config {
	endpoint := "http://unused/v1/events"
	if srv != nil {
		endpoint = srv.URL + "/v1/events"
	}
	return config.Config{TelemetryEndpoint: endpoint, OrgTokens: map[string]string{"acme": token}}
}

// TestFetchRepoBriefSendsSctxUserAgent asserts the session-start surface call
// identifies itself as sctx traffic rather than the default Go-http-client.
func TestFetchRepoBriefSendsSctxUserAgent(t *testing.T) {
	const token = "tok-abc"
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, briefFixture)
	}))
	defer srv.Close()

	if _, err := fetchRepoBrief(cfgFor(srv, token), token, "acme/widgets", sessionStartCall{}, "9.9.9"); err != nil {
		t.Fatalf("fetchRepoBrief: %v", err)
	}
	want := httpclient.UserAgent("9.9.9", "claude-code")
	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}

func runSessionStart(t *testing.T, cfg config.Config, cwd, source string) string {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(fmt.Sprintf(`{"session_id":"sess-1","cwd":%q,"source":%q}`, cwd, source))
	if code := RunClaudeSessionStart(in, &out, cfg, "1.2.3"); code != 0 {
		t.Fatalf("exit code = %d, want 0; this hook must never fail a session", code)
	}
	return out.String()
}

func TestSessionBriefNamesTheRepoTheToolsAndTheMemory(t *testing.T) {
	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	// Same sha the fixture reports as indexed, so the freshness line is the
	// "equal" branch — the one that licenses trusting retrieval as-is.
	root := fakeRepo(t, "abcdef0123456789")
	srv := briefServer(t, token, briefFixture, http.StatusOK)

	got := runSessionStart(t, cfgFor(srv, token), root, "startup")

	for _, want := range []string{
		"=== SynapCTX brief — acme/widgets",
		"local HEAD abcdef0123 (main)",
		"indexed abcdef0123 (main, 2026-09-01)",
		"also tracked: develop",
		// The server name comes from .claude.json, not from the org slug: a tool
		// name the agent cannot call teaches it to distrust the whole brief.
		"mcp__acme-graph__retrieve_context",
		"mcp__acme-graph__recall_memory",
		"deferred is not unavailable",
		"Org memory about this repository (1 notes, newest first):",
		"[decision · 2026-08-20]",
		"uppercase constraint was dropped",
		"Record what you decide here with store_memory",
		"Local HEAD equals the indexed sha",
		"Try first: retrieve_context {query: \"acme/widgets entry points and service boundaries\", repository: \"acme/widgets\"}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief is missing %q:\n%s", want, got)
		}
	}
	// SessionStart injects PLAIN stdout as context; a JSON envelope would be
	// shown to the model verbatim.
	if strings.Contains(got, "hookSpecificOutput") {
		t.Errorf("brief was wrapped in a hook envelope, which SessionStart does not read:\n%s", got)
	}
}

// The other half of the freshness line, and the one that matters more: an agent
// that does not know retrieval is describing a different commit will trust a
// stale answer about code it just rewrote.
func TestSessionBriefSaysWhenLocalHeadDiffersFromTheIndex(t *testing.T) {
	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "1111111111222222")
	srv := briefServer(t, token, briefFixture, http.StatusOK)

	got := runSessionStart(t, cfgFor(srv, token), root, "startup")

	if !strings.Contains(got, "Local HEAD differs from the indexed sha: retrieval describes abcdef0123") {
		t.Errorf("brief does not warn that the index is behind the checkout:\n%s", got)
	}
	if !strings.Contains(got, "sctx watch") {
		t.Errorf("brief warns about drift without naming the fix:\n%s", got)
	}
}

// A repository with no memory yet gets a line saying so, not silence: silence
// reads as "SynapCTX has nothing for you", when it means "you are the first".
func TestSessionBriefInvitesTheFirstMemory(t *testing.T) {
	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")
	srv := briefServer(t, token, `{"repository":{"name":"acme/widgets"}}`, http.StatusOK)

	got := runSessionStart(t, cfgFor(srv, token), root, "startup")

	if !strings.Contains(got, "No org memory is bound to this repository yet") {
		t.Errorf("an empty brief said nothing about being empty:\n%s", got)
	}
	// No refs and no HEAD match means no freshness claim at all — a line we
	// cannot support must be dropped, not printed with a placeholder.
	if strings.Contains(got, "indexed ") {
		t.Errorf("brief claimed an indexed sha it was never given:\n%s", got)
	}
	// The fixture carries no retrievalHint (an older proxy, or one that could
	// not resolve the repository) — the line must be dropped, not printed empty.
	if strings.Contains(got, "Try first:") {
		t.Errorf("brief printed a retrieval hint line with nothing behind it:\n%s", got)
	}
}

// Compaction happens under context pressure, which is the worst possible moment
// to spend three seconds on a network call. The reminder is free.
func TestCompactSourceRemindsWithoutAskingTheServer(t *testing.T) {
	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, briefFixture)
	}))
	defer srv.Close()

	got := runSessionStart(t, cfgFor(srv, token), root, "compact")

	if called {
		t.Error("a compaction triggered a network call; it must be answered from nothing")
	}
	if !strings.Contains(got, "mcp__acme-graph__recall_memory") || !strings.Contains(got, "store_memory") {
		t.Errorf("compaction reminder does not name the tools:\n%s", got)
	}
}

// No key means no organization to ask. `sctx setup` owns that conversation; a
// configuration complaint here lands in front of an agent that cannot act on it.
func TestSessionBriefIsSilentWithoutAKeyForTheOrg(t *testing.T) {
	fakeClaudeConfig(t, "tok-abc", "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")

	cfg := config.Config{TelemetryEndpoint: "http://unused/v1/events", OrgTokens: map[string]string{"other": "tok"}}
	if got := runSessionStart(t, cfg, root, "startup"); got != "" {
		t.Errorf("brief printed something without a key for this org:\n%s", got)
	}
}

// Fail open: a server error is not the developer's problem at session start.
func TestSessionBriefIsSilentOnAServerError(t *testing.T) {
	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")
	srv := briefServer(t, token, `{"error":"boom"}`, http.StatusInternalServerError)

	if got := runSessionStart(t, cfgFor(srv, token), root, "startup"); got != "" {
		t.Errorf("a 500 produced output:\n%s", got)
	}
}

// Outside a git repository there is no repository to brief, and no org to route
// the key by. Silence, exit 0.
func TestSessionBriefIsSilentOutsideARepository(t *testing.T) {
	fakeClaudeConfig(t, "tok-abc", "acme-graph")
	if got := runSessionStart(t, cfgFor(nil, "tok-abc"), t.TempDir(), "startup"); got != "" {
		t.Errorf("brief printed something outside a repository:\n%s", got)
	}
}

// A note is orientation, but it is not the session. Long notes are cut on a rune
// boundary and marked, so nothing reads as complete when it was truncated.
func TestSessionNotesAreCappedInLengthAndCount(t *testing.T) {
	long := strings.Repeat("é", sessionNoteMaxChars+50)
	if got := truncateRunes(long, sessionNoteMaxChars); len([]rune(got)) != sessionNoteMaxChars+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncateRunes produced %d runes, want %d plus an ellipsis", len([]rune(got)), sessionNoteMaxChars)
	}
	if got := truncateRunes("short", sessionNoteMaxChars); got != "short" {
		t.Errorf("truncateRunes altered a short note: %q", got)
	}

	var notes strings.Builder
	for i := range sessionNoteMax + 4 {
		if i > 0 {
			notes.WriteString(",")
		}
		fmt.Fprintf(&notes, `{"id":"n%d","kind":"lesson","createdAt":"2026-08-20T09:00:00Z","text":"note-%d"}`, i, i)
	}
	body := fmt.Sprintf(`{"repository":{"name":"acme/widgets"},"notes":[%s]}`, notes.String())

	const token = "tok-abc"
	fakeClaudeConfig(t, token, "acme-graph")
	root := fakeRepo(t, "abcdef0123456789")
	srv := briefServer(t, token, body, http.StatusOK)

	got := runSessionStart(t, cfgFor(srv, token), root, "startup")
	if n := strings.Count(got, "• ["); n != sessionNoteMax {
		t.Errorf("brief rendered %d notes, want the cap of %d:\n%s", n, sessionNoteMax, got)
	}
}

// packed-refs is the state a repository reaches after `git gc`, and a brief that
// silently lost its HEAD line there would look like a server problem.
func TestLocalHeadFallsBackToPackedRefs(t *testing.T) {
	root := fakeRepo(t, "unused")
	git := filepath.Join(root, ".git")
	if err := os.Remove(filepath.Join(git, "refs", "heads", "main")); err != nil {
		t.Fatal(err)
	}
	packed := "# pack-refs with: peeled fully-peeled sorted \n" +
		"deadbeefcafe0000 refs/heads/main\n" +
		"^0000000000000000\n"
	if err := os.WriteFile(filepath.Join(git, "packed-refs"), []byte(packed), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, branch := localHead(root)
	if sha != "deadbeefca" || branch != "main" {
		t.Errorf("localHead = (%q, %q), want (deadbeefca, main)", sha, branch)
	}
}

// A detached HEAD has a sha and no branch. Both halves are optional in the
// header, so this must not produce an empty "()" either.
func TestLocalHeadHandlesDetachedHead(t *testing.T) {
	root := fakeRepo(t, "abcdef0123456789")
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, branch := localHead(root)
	if sha != "0123456789" || branch != "" {
		t.Errorf("localHead = (%q, %q), want (0123456789, \"\")", sha, branch)
	}
}

// A linked worktree (and a submodule) has `.git` as a FILE. Hard-coding
// `<root>/.git` as a directory read nothing there and the freshness line
// vanished — on precisely the checkout least likely to match the indexed ref,
// since a worktree is usually a branch that main does not have.
//
// HEAD is per-worktree; refs and packed-refs live in the COMMON directory named
// by the `commondir` file beside it.
func TestLocalHeadFollowsAWorktreeGitFileToTheCommonDir(t *testing.T) {
	base := t.TempDir()
	common := filepath.Join(base, "main", ".git")
	worktreeGit := filepath.Join(common, "worktrees", "feature")
	root := filepath.Join(base, "feature")
	for _, dir := range []string{filepath.Join(common, "refs", "heads"), worktreeGit, root} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The worktree's git dir: HEAD, and a commondir pointing back at the shared
	// directory the way git writes it — relative, "../..".
	write(filepath.Join(worktreeGit, "HEAD"), "ref: refs/heads/feature\n")
	write(filepath.Join(worktreeGit, "commondir"), "../..\n")
	// The shared directory: config (so the repository can be NAMED at all) and
	// the ref itself, neither of which exists in the worktree's own git dir.
	write(filepath.Join(common, "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n")
	write(filepath.Join(common, "refs", "heads", "feature"), "feedfacecafebabe\n")
	write(filepath.Join(root, ".git"), "gitdir: "+worktreeGit+"\n")

	sha, branch := localHead(root)
	if sha != "feedfaceca" || branch != "feature" {
		t.Errorf("localHead = (%q, %q), want (feedfaceca, feature)", sha, branch)
	}
	// And the repository must still be identifiable, which needs config from the
	// common dir too — otherwise the hook never reaches localHead at all.
	if _, name, ok := gitrepo.RootAndName(root); !ok || name != "acme/widgets" {
		t.Errorf("RootAndName in a worktree = (%q, %v), want (acme/widgets, true)", name, ok)
	}
}
