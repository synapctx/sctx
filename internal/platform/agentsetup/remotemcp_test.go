package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

func kiloAgent(t *testing.T) Agent {
	t.Helper()
	a, ok := agentdoc.AgentByID("kilocode")
	if !ok {
		t.Fatal("kilocode row missing from KnownAgents")
	}
	return a
}

// The registration must land BESIDE what the customer already configured, not
// on top of it. A config file is the one file whose corruption stops the agent
// from starting, so every key we do not manage has to survive untouched — and a
// second run has to change nothing at all, or `sctx setup` can never be safely
// re-run.
func TestRemoteMCPInstallPreservesTheirServersAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	a := kiloAgent(t)
	path := filepath.Join(home, filepath.FromSlash(a.MCPConfig))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"$schema":"https://app.kilo.ai/config.json","model":"kilo/x","mcp":{"context7":{"command":["npx","-y","@upstash/context7-mcp"],"type":"local"}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tokens := map[string]string{"acme": "sctx_live_acme"}
	changed, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v, want one line", changed)
	}

	var doc struct {
		Schema string                     `json:"$schema"`
		Model  string                     `json:"model"`
		MCP    map[string]json.RawMessage `json:"mcp"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("we wrote invalid JSON — the agent would not start: %v\n%s", err, raw)
	}
	if doc.Schema == "" || doc.Model != "kilo/x" {
		t.Errorf("keys we do not manage were lost:\n%s", raw)
	}
	if _, ok := doc.MCP["context7"]; !ok {
		t.Errorf("their own MCP server was dropped:\n%s", raw)
	}
	entry, ok := doc.MCP["synapctx-acme"]
	if !ok {
		t.Fatalf("our server was not registered:\n%s", raw)
	}
	var got struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Enabled bool              `json:"enabled"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(entry, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "remote" || got.URL != "https://mcp.synapctx.com/mcp" || !got.Enabled {
		t.Errorf("registration = %+v, want an enabled remote entry at the /mcp endpoint", got)
	}
	if got.Headers["Authorization"] != "Bearer sctx_live_acme" {
		t.Errorf("credential header not written: %+v", got.Headers)
	}
	// It holds a live credential, exactly like the Codex TOML.
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 for a file containing a token", info.Mode().Perm())
	}

	st, err := InspectRemoteMCP(home, a, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Complete() {
		t.Fatalf("still incomplete after installing: %+v", st)
	}
	again, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("second install changed something: %v", again)
	}
}

// A rotated key or a moved endpoint must reach the file, or the agent keeps
// authenticating with a credential that no longer works — which looks exactly
// like the platform being down.
func TestRemoteMCPInstallUpdatesOurOwnRegistrationInPlace(t *testing.T) {
	home := t.TempDir()
	a := kiloAgent(t)
	if err := os.MkdirAll(filepath.Join(home, ".config", "kilo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_new"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(a.MCPConfig)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sctx_live_old") {
		t.Errorf("the superseded credential is still in the file:\n%s", raw)
	}
	if !strings.Contains(string(raw), "sctx_live_new") {
		t.Errorf("the current credential was not written:\n%s", raw)
	}
}

// The one thing worse than not registering a server is redirecting one the
// customer registered themselves. With no comment to mark ownership, an entry
// aimed at another host is theirs, and we stop.
func TestRemoteMCPRefusesAnEntryPointingSomewhereElse(t *testing.T) {
	home := t.TempDir()
	a := kiloAgent(t)
	path := filepath.Join(home, filepath.FromSlash(a.MCPConfig))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := `{"mcp":{"synapctx-acme":{"type":"remote","url":"https://internal.example/mcp"}}}`
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"acme": "sctx_live_acme"}

	st, err := InspectRemoteMCP(home, a, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Conflicts) == 0 {
		t.Fatalf("a foreign registration under our name was not reported: %+v", st)
	}
	if _, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", tokens); err == nil {
		t.Error("install overwrote a registration it does not own")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(after)) != theirs {
		t.Errorf("their file was modified:\n%s", after)
	}
}

// Both Kilo and OpenCode deep-merge every config they find, later wins. A
// `.jsonc` naming our server therefore beats the `.json` we just wrote — and we
// cannot rewrite the .jsonc without stripping its comments, so the only honest
// outcome is to say so.
func TestRemoteMCPReportsAJsoncThatWouldOverrideUs(t *testing.T) {
	home := t.TempDir()
	a := kiloAgent(t)
	dir := filepath.Join(home, ".config", "kilo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := "{\n  // ours, hand written\n  \"mcp\": { \"synapctx-acme\": { \"enabled\": false } }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "kilo.jsonc"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := InspectRemoteMCP(home, a, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Conflicts) == 0 || !strings.Contains(strings.Join(st.Conflicts, " "), "kilo.jsonc") {
		t.Errorf("an overriding .jsonc was not reported: %+v", st.Conflicts)
	}
}

// An unparseable config is the case where writing is most tempting and most
// destructive: we cannot round-trip what we cannot read, so we report and stop.
func TestRemoteMCPNeverWritesOverAConfigItCannotParse(t *testing.T) {
	home := t.TempDir()
	a := kiloAgent(t)
	path := filepath.Join(home, filepath.FromSlash(a.MCPConfig))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "{ not json at all"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"acme": "sctx_live_acme"}
	st, err := InspectRemoteMCP(home, a, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatalf("one unreadable config must not fail inspection: %v", err)
	}
	if !st.Unreadable || st.Complete() {
		t.Errorf("unreadable config reported as fine: %+v", st)
	}
	if _, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", tokens); err == nil {
		t.Error("install proceeded over a config it could not parse")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Errorf("the file was rewritten:\n%s", after)
	}
}

// MOVING THE ENDPOINT MUST NOT MAKE SCTX DISOWN ITS OWN REGISTRATION.
//
// Ownership used to be "points at the endpoint we are configured with", which
// fails at precisely the moment it matters: change the endpoint and every entry
// sctx wrote becomes foreign, so the rewrite that the change requires is the one
// thing refused. Seen for real when this machine moved from the local dev proxy
// to the hosted MCP host.
func TestRemoteMCPFollowsItsOwnRegistrationWhenTheEndpointMoves(t *testing.T) {
	home := t.TempDir()
	a := kiloAgent(t)
	if err := os.MkdirAll(filepath.Join(home, ".config", "kilo"), 0o755); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"acme": "sctx_live_acme"}
	if _, err := InstallRemoteMCP(home, a, "http://127.0.0.1:6220", tokens); err != nil {
		t.Fatal(err)
	}

	st, err := InspectRemoteMCP(home, a, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Conflicts) != 0 {
		t.Fatalf("sctx disowned its own registration after an endpoint change: %v", st.Conflicts)
	}
	if !st.Stale {
		t.Error("an entry pointing at the previous endpoint was not reported stale")
	}
	if _, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", tokens); err != nil {
		t.Fatalf("install refused to move its own entry: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(a.MCPConfig)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "127.0.0.1") || !strings.Contains(string(raw), "https://mcp.synapctx.com/mcp") {
		t.Errorf("the registration still points at the old host:\n%s", raw)
	}
}

// EVERY DIALECT IN THE TABLE MUST PRODUCE WHAT THAT CLIENT DOCUMENTS.
//
// Five clients spell the same registration four ways, and the cost of getting
// one wrong is silent: the file is valid JSON, the client ignores or rejects the
// entry, and `sctx setup` reports it registered. This pins each spelling to the
// documentation it came from.
func TestEachClientGetsItsOwnDocumentedSpelling(t *testing.T) {
	for _, tc := range []struct {
		agent    string
		key      string
		urlField string
		wantType string
	}{
		{"kilocode", "mcp", "url", "remote"},
		{"opencode", "mcp", "url", "remote"},
		{"gemini", "mcpServers", "httpUrl", ""},
		{"windsurf", "mcpServers", "serverUrl", ""},
		{"crush", "mcp", "url", "http"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			a, ok := agentdoc.AgentByID(tc.agent)
			if !ok {
				t.Fatalf("%s missing from KnownAgents", tc.agent)
			}
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(filepath.Join(home, filepath.FromSlash(a.MCPConfig))), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := InstallRemoteMCP(home, a, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_acme"}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(a.MCPConfig)))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]map[string]map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("invalid JSON for %s: %v", tc.agent, err)
			}
			entry, ok := doc[tc.key]["synapctx-acme"]
			if !ok {
				t.Fatalf("no registration under %q:\n%s", tc.key, raw)
			}
			if entry[tc.urlField] != "https://mcp.synapctx.com/mcp" {
				t.Errorf("%s is not named by %q: %v", tc.agent, tc.urlField, entry)
			}
			if got, _ := entry["type"].(string); got != tc.wantType {
				t.Errorf("type = %q, want %q", got, tc.wantType)
			}
			// A member the client does not document is how a schema-validated
			// config rejects the whole entry.
			if _, has := entry["enabled"]; has != a.MCPEnabled {
				t.Errorf("enabled present = %v, want %v", has, a.MCPEnabled)
			}
			// Re-reading its own work must not report a conflict, or the second
			// install refuses to touch the file it just wrote.
			st, err := InspectRemoteMCP(home, a, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_acme"})
			if err != nil {
				t.Fatal(err)
			}
			if !st.Complete() {
				t.Errorf("its own registration was not recognised: %+v", st)
			}
		})
	}
}
