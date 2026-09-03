package hook

import (
	"os"
	"path/filepath"
	"testing"
)

// The server name is read from the developer's own config because it is the only
// place that knows it. Matching is on the CREDENTIAL, not on a name pattern,
// which is what makes the owner's machine work: there the servers are called
// `parlitrack` and `cloudresty`, with no `synapctx-` prefix at all.
func TestClaudeServerNameForMatchesOnTheCredential(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{
	  "projects": {"/some/path": {"history": ["irrelevant"]}},
	  "mcpServers": {
	    "context7": {"type": "http", "url": "https://c7", "headers": {"Authorization": "Bearer other"}},
	    "parlitrack": {"type": "http", "url": "https://mcp", "headers": {"authorization": "Bearer tok-abc"}}
	  }
	}`)

	if got := claudeServerNameFor(home, root, root, "tok-abc"); got != "parlitrack" {
		t.Errorf("claudeServerNameFor = %q, want %q (header name case must not matter)", got, "parlitrack")
	}
	// A token no registered server carries must NOT fall through to some other
	// server's name — the caller would then print tool names for the wrong org.
	if got := claudeServerNameFor(home, root, root, "tok-unknown"); got != "" {
		t.Errorf("an unmatched token resolved to %q, want \"\"", got)
	}

	// Every failure is silent and empty: the caller falls back to the org slug,
	// and a hook that crashed on a malformed config would break session start.
	write(`{ not json`)
	if got := claudeServerNameFor(home, root, root, "tok-abc"); got != "" {
		t.Errorf("malformed .claude.json returned %q, want \"\"", got)
	}
	if got := claudeServerNameFor(t.TempDir(), root, root, "tok-abc"); got != "" {
		t.Errorf("missing .claude.json returned %q, want \"\"", got)
	}
	if got := claudeServerNameFor(home, root, root, ""); got != "" {
		t.Errorf("an empty token matched %q; it must never match an empty header", got)
	}
}

// `claude mcp add` defaults to LOCAL scope, which is stored per directory under
// `projects.<dir>.mcpServers` — so a search of top-level `mcpServers` alone
// finds nothing on the most common install and the brief silently guesses.
func TestClaudeServerNameForFindsLocalAndProjectScopes(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "acme", "widgets")
	nested := filepath.Join(root, "internal", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("local scope, matched by an ancestor directory", func(t *testing.T) {
		body := `{"projects":{` +
			`"` + jsonPath(filepath.Dir(root)) + `":{"mcpServers":{"parent-graph":{"headers":{"Authorization":"Bearer tok-abc"}}}},` +
			`"` + jsonPath(root) + `":{"mcpServers":{"widgets-graph":{"headers":{"Authorization":"Bearer tok-abc"}}}},` +
			`"` + jsonPath("/somewhere/else") + `":{"mcpServers":{"unrelated":{"headers":{"Authorization":"Bearer tok-abc"}}}}` +
			`}}`
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		// The agent's cwd is BELOW the root, so the entry is found by ancestry,
		// not by equality — and the most specific ancestor wins, because a
		// parent directory may hold a different credential entirely.
		if got := claudeServerNameFor(home, nested, root, "tok-abc"); got != "widgets-graph" {
			t.Errorf("local-scope lookup = %q, want widgets-graph", got)
		}
		// An unrelated project's registration names a server this session cannot
		// call; it must never be borrowed.
		if got := claudeServerNameFor(home, t.TempDir(), "", "tok-abc"); got != "" {
			t.Errorf("borrowed an unrelated project's server %q", got)
		}
	})

	t.Run("project scope from the repository's .mcp.json", func(t *testing.T) {
		// User scope holds a DIFFERENT name for the same credential, so this also
		// proves the precedence: the checked-in project file wins over global.
		userBody := `{"mcpServers":{"user-graph":{"headers":{"Authorization":"Bearer tok-abc"}}}}`
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(userBody), 0o600); err != nil {
			t.Fatal(err)
		}
		projectBody := `{"mcpServers":{"repo-graph":{"type":"http","headers":{"Authorization":"Bearer tok-abc"}}}}`
		if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(projectBody), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := claudeServerNameFor(home, nested, root, "tok-abc"); got != "repo-graph" {
			t.Errorf("project-scope lookup = %q, want repo-graph", got)
		}
		// With no root there is no project file to read, and user scope answers.
		if got := claudeServerNameFor(home, nested, "", "tok-abc"); got != "user-graph" {
			t.Errorf("fallback to user scope = %q, want user-graph", got)
		}
	})
}

// jsonPath escapes a filesystem path for embedding in a JSON string literal.
// Windows separators would otherwise be read as escape sequences.
func jsonPath(p string) string {
	var out []rune
	for _, r := range p {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
