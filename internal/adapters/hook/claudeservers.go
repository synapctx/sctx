package hook

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
)

// mcpScope is the narrowest shape that answers "which server carries this
// token". `~/.claude.json` accumulates per-project history and session state,
// and none of the rest of it is our business.
type mcpScope struct {
	MCPServers map[string]struct {
		Headers map[string]string `json:"headers"`
	} `json:"mcpServers"`
}

// claudeServerNameFor answers the one question the session brief cannot guess:
// what THIS machine's Claude Code calls the SynapCTX MCP server for this
// organization, because that name is what the tool namespace is built from.
//
// Naming it matters more than it looks. A brief that says `mcp__synapctx-acme__
// retrieve_context` on a machine where the server was registered as `acme`
// names a tool that does not exist, and an agent that tries one non-existent
// tool stops trusting the rest of the brief. The developer chose the key when
// they added the server; only their config knows it.
//
// The server is identified by its CREDENTIAL, not by its name: the entry whose
// Authorization header carries this org's token is this org's server, whatever
// it was called. Matching on a name pattern would fail on exactly the machine
// this exists for — the owner's, where the servers are `parlitrack` and
// `cloudresty` with no prefix at all.
//
// ALL THREE SCOPES are searched, in Claude Code's own precedence order, because
// `claude mcp add` defaults to LOCAL and a user-scope-only search finds nothing
// on the most common install:
//
//	local   ~/.claude.json  projects.<dir>.mcpServers   (per directory)
//	project <root>/.mcp.json  mcpServers                (checked in, shared)
//	user    ~/.claude.json  mcpServers                  (global)
//
// Both files are READ and never written. Any failure returns "" and the caller
// falls back to the org slug — a slightly wrong name in prose is recoverable, a
// crashed session-start hook is not. The token is never logged or printed.
func claudeServerNameFor(home, cwd, root, token string) string {
	if token == "" {
		return ""
	}
	var user mcpScope
	var projects map[string]mcpScope
	if home != "" {
		var doc struct {
			MCPServers map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
			Projects map[string]mcpScope `json:"projects"`
		}
		if raw, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil {
			if json.Unmarshal(raw, &doc) == nil {
				user.MCPServers = doc.MCPServers
				projects = doc.Projects
			}
		}
	}

	// Local scope first, and the most specific project key wins: the same server
	// may be registered for a parent directory with a different credential, and
	// the entry closest to where the agent is standing is the one in force.
	if name := localScopeServer(projects, cwd, root, token); name != "" {
		return name
	}
	if root != "" {
		var project mcpScope
		if raw, err := os.ReadFile(filepath.Join(root, ".mcp.json")); err == nil {
			if json.Unmarshal(raw, &project) == nil {
				if name := serverWithBearer(project, token); name != "" {
					return name
				}
			}
		}
	}
	return serverWithBearer(user, token)
}

// localScopeServer searches `projects.<dir>.mcpServers`, preferring the longest
// key that is cwd/root or an ancestor of one of them. An unrelated project's
// entry is never used: it would name a server this session cannot call.
func localScopeServer(projects map[string]mcpScope, cwd, root, token string) string {
	var bestName string
	var bestLen int
	for dir, scope := range projects {
		if !dirCovers(dir, cwd) && !dirCovers(dir, root) {
			continue
		}
		name := serverWithBearer(scope, token)
		if name == "" {
			continue
		}
		if len(dir) > bestLen {
			bestName, bestLen = name, len(dir)
		}
	}
	return bestName
}

// dirCovers reports whether dir is target or one of its ancestors. Compared on
// cleaned paths with a separator guard, so `/a/bc` is not read as an ancestor of
// `/a/bcd`.
func dirCovers(dir, target string) bool {
	if dir == "" || target == "" {
		return false
	}
	dir = filepath.Clean(dir)
	target = filepath.Clean(target)
	if dir == target {
		return true
	}
	return strings.HasPrefix(target, dir+string(filepath.Separator))
}

func serverWithBearer(scope mcpScope, token string) string {
	want := "Bearer " + token
	for name, srv := range scope.MCPServers {
		for key, value := range srv.Headers {
			// Header names are case-insensitive on the wire and clients
			// round-trip whatever case the developer typed.
			if strings.EqualFold(key, "Authorization") && value == want {
				return name
			}
		}
	}
	return ""
}
