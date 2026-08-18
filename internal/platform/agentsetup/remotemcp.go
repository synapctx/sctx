package agentsetup

// MCP registration for agents whose servers live in a JSON config file, as an
// `mcp` object of remote entries. Codex has its own file (codexmcp.go) because
// its registry is TOML with ownership markers; JSON cannot carry a comment, so
// ownership here is decided by what the entry POINTS AT rather than by a marker
// we wrote.
//
// Why this exists at all: instructions and capability fail independently, and
// until 2026-08-18 only Codex had the capability half. Kilo Code and OpenCode
// were handed SYNAPCTX.md — a document whose entire subject is a set of tools —
// with no registration anywhere, so every trigger in it named a tool the agent
// could not call. That is worse than silence: an agent that tries a tool which
// does not exist learns to distrust the file that named it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

// RemoteMCPStatus is the capability half of setup for one JSON-configured agent.
type RemoteMCPStatus struct {
	AgentID    string
	AgentName  string
	ConfigPath string
	Servers    []string
	Installed  bool
	Stale      bool
	// Conflicts are registrations under a name we want that we must not touch:
	// they point somewhere else, or they are defined in a sibling `.jsonc` whose
	// comments we cannot rewrite and which wins the deep merge anyway.
	Conflicts []string
	// Unreadable is set when the config file exists but is not JSON we can parse.
	// We then write nothing at all — a config file is the one thing whose
	// corruption stops the agent from starting.
	Unreadable bool
}

// Complete means every desired registration is present, current, and unopposed.
func (s RemoteMCPStatus) Complete() bool {
	return len(s.Servers) == 0 ||
		(s.Installed && !s.Stale && len(s.Conflicts) == 0 && !s.Unreadable)
}

// remoteMCPEntry renders one registration in the dialect the agent's row
// declares. Every client says the same three things — where the server is, what
// to send with the request, whether it is on — and disagrees only about the
// spelling, so the spelling is data (agentdoc.Agent) and this is one writer.
//
// Members the client does not document are OMITTED rather than sent as empty:
// several of these files are schema-validated, and an unknown key is how a
// registration gets rejected wholesale.
func remoteMCPEntry(a Agent, endpoint, token string) map[string]any {
	entry := map[string]any{
		urlField(a): normalizeMCPEndpoint(endpoint),
		"headers":   map[string]string{"Authorization": "Bearer " + token},
	}
	if a.MCPType != "" {
		entry["type"] = a.MCPType
	}
	if a.MCPEnabled {
		entry["enabled"] = true
	}
	return entry
}

func urlField(a Agent) string {
	if a.MCPURLField != "" {
		return a.MCPURLField
	}
	return "url"
}

func serversKey(a Agent) string {
	if a.MCPKey != "" {
		return a.MCPKey
	}
	return "mcp"
}

// urlOf reads an existing entry's endpoint. It tries the agent's own spelling
// first and then every other one we know: a customer may have registered the
// same server by hand under a different-but-valid member, and reading only our
// spelling would call their entry foreign and refuse to touch it.
func urlOf(entry map[string]any, a Agent) string {
	for _, field := range []string{urlField(a), "url", "httpUrl", "serverUrl"} {
		if v, ok := entry[field].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// InspectRemoteMCP reports what this agent's JSON config says about the servers
// our credentials require. Tokens are compared in memory and never reported.
func InspectRemoteMCP(home string, a Agent, endpoint string, orgTokens map[string]string) (RemoteMCPStatus, error) {
	if home == "" {
		return RemoteMCPStatus{}, errors.New("no home directory")
	}
	if a.MCP != agentdoc.MCPRemoteJSON || a.MCPConfig == "" {
		return RemoteMCPStatus{}, fmt.Errorf("agent %s has no JSON MCP registry", a.ID)
	}
	st := RemoteMCPStatus{
		AgentID:    a.ID,
		AgentName:  a.Name,
		ConfigPath: filepath.Join(home, filepath.FromSlash(a.MCPConfig)),
		Servers:    remoteMCPServerNames(orgTokens),
	}
	if len(st.Servers) == 0 {
		return st, nil
	}

	_, existing, err := readRemoteMCPConfig(st.ConfigPath, serversKey(a))
	if err != nil {
		// Reported, never returned: one unparseable agent config must not fail
		// the whole inspection, and it is exactly the case where we must not
		// write.
		st.Unreadable = true
		return st, nil
	}

	desired := desiredRemoteEntries(a, endpoint, orgTokens)
	present, stale := 0, false
	for _, name := range st.Servers {
		raw, ok := existing[name]
		if !ok {
			continue
		}
		if !ownedRemoteEntry(raw, a, endpoint) {
			st.Conflicts = append(st.Conflicts, name)
			continue
		}
		present++
		if !sameJSON(raw, desired[name]) {
			stale = true
		}
	}
	st.Installed = present > 0 && present+len(st.Conflicts) == len(st.Servers)
	st.Stale = stale
	st.Conflicts = append(st.Conflicts, overridingSiblings(st.ConfigPath, st.Servers)...)
	sort.Strings(st.Conflicts)
	return st, nil
}

// InstallRemoteMCP writes the registrations sctx owns into the agent's JSON
// config, preserving every other key and every other server exactly.
func InstallRemoteMCP(home string, a Agent, endpoint string, orgTokens map[string]string) ([]string, error) {
	st, err := InspectRemoteMCP(home, a, endpoint, orgTokens)
	if err != nil {
		return nil, err
	}
	if len(st.Servers) == 0 || st.Complete() {
		return nil, nil
	}
	if st.Unreadable {
		return nil, fmt.Errorf("%s is not valid JSON; left unchanged", st.ConfigPath)
	}
	if len(st.Conflicts) > 0 {
		return nil, fmt.Errorf("%s: %s already registered elsewhere or pointing at another endpoint; left unchanged",
			st.ConfigPath, strings.Join(st.Conflicts, ", "))
	}

	top, existing, err := readRemoteMCPConfig(st.ConfigPath, serversKey(a))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", st.ConfigPath, err)
	}
	for name, entry := range desiredRemoteEntries(a, endpoint, orgTokens) {
		existing[name] = entry
	}
	merged, err := json.Marshal(orderedJSONObject(existing))
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", st.ConfigPath, err)
	}
	top[serversKey(a)] = merged

	body, err := marshalRemoteMCPConfig(top)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", st.ConfigPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(st.ConfigPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(st.ConfigPath), err)
	}
	// 0600 for the same reason as the Codex TOML: the Authorization header is a
	// live credential.
	if err := writePrivateFile(st.ConfigPath, body); err != nil {
		return nil, fmt.Errorf("writing %s: %w", st.ConfigPath, err)
	}
	verb := "registered"
	if st.Installed {
		verb = "updated"
	}
	return []string{fmt.Sprintf("%s %d SynapCTX MCP server(s) for %s (%s)",
		verb, len(st.Servers), st.AgentName, st.ConfigPath)}, nil
}

func remoteMCPServerNames(orgTokens map[string]string) []string {
	names := make([]string, 0, len(orgTokens))
	for org, token := range orgTokens {
		if strings.TrimSpace(org) == "" || strings.TrimSpace(token) == "" {
			continue
		}
		names = append(names, "synapctx-"+strings.TrimSpace(org))
	}
	sort.Strings(names)
	return names
}

func desiredRemoteEntries(a Agent, endpoint string, orgTokens map[string]string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(orgTokens))
	for org, token := range orgTokens {
		org, token = strings.TrimSpace(org), strings.TrimSpace(token)
		if org == "" || token == "" {
			continue
		}
		raw, err := json.Marshal(remoteMCPEntry(a, endpoint, token))
		if err != nil {
			continue
		}
		out["synapctx-"+org] = raw
	}
	return out
}

// normalizeMCPEndpoint matches the Codex writer exactly, so one endpoint setting
// cannot mean two different URLs depending on which agent read it.
func normalizeMCPEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if !strings.HasSuffix(endpoint, "/mcp") {
		endpoint += "/mcp"
	}
	return endpoint
}

// ownedRemoteEntry decides whether an existing registration under one of our
// names is ours to rewrite.
//
// JSON cannot hold the ownership comment the TOML writer uses, so ownership is
// inferred from the entry itself: a remote server (never a local command) that
// either points at the endpoint we are configured with, or carries an
// sctx-issued `sctx_live_` bearer token. Anything else under one of our names is
// the customer's, and a collision is reported rather than resolved.
//
// THE TOKEN CLAUSE IS NOT REDUNDANT, and leaving it out was a real bug: judging
// ownership by the CURRENT endpoint means that the moment the endpoint changes —
// the exact moment a rewrite is needed — sctx stops recognising its own entry
// and refuses to touch it. Observed on 2026-08-18 when this machine moved from
// the local dev proxy to the hosted host: four registrations sctx had written
// minutes earlier were all reported as foreign. An `sctx_live_` credential is
// something only this tool puts in a config file, and it survives the endpoint
// moving.
func ownedRemoteEntry(raw json.RawMessage, a Agent, endpoint string) bool {
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		return false
	}
	// A local server launched by a command is never ours: everything sctx writes
	// is remote.
	if _, isLocal := entry["command"]; isLocal {
		return false
	}
	if kind, ok := entry["type"].(string); ok && a.MCPType != "" && kind != a.MCPType {
		return false
	}
	if sameHost(urlOf(entry, a), normalizeMCPEndpoint(endpoint)) {
		return true
	}
	headers, _ := entry["headers"].(map[string]any)
	auth, _ := headers["Authorization"].(string)
	return strings.HasPrefix(strings.TrimSpace(auth), "Bearer sctx_live_")
}

func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Host, ub.Host)
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// overridingSiblings reports a `.jsonc` beside the file we write that already
// names one of our servers.
//
// This is not fussiness. Kilo and OpenCode deep-merge every config they find and
// the LATER file wins, so a `synapctx-acme` in kilo.jsonc silently overrides the
// one we just wrote to kilo.json — and we cannot rewrite the .jsonc, because
// re-encoding it would strip the comments that are its whole reason to exist.
// Reporting the collision is the only honest outcome.
func overridingSiblings(configPath string, servers []string) []string {
	sibling := strings.TrimSuffix(configPath, ".json") + ".jsonc"
	if sibling == configPath {
		return nil
	}
	raw, err := os.ReadFile(sibling)
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range servers {
		if strings.Contains(string(raw), `"`+name+`"`) {
			out = append(out, name+" (also in "+filepath.Base(sibling)+")")
		}
	}
	return out
}

// readRemoteMCPConfig returns the whole document and its `mcp` object, both as
// raw members so every key we do not manage survives a rewrite byte-for-byte.
func readRemoteMCPConfig(path, serversKey string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return top, servers, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return top, servers, nil
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, nil, err
	}
	if existing, ok := top[serversKey]; ok {
		if err := json.Unmarshal(existing, &servers); err != nil {
			return nil, nil, err
		}
	}
	return top, servers, nil
}

// marshalRemoteMCPConfig renders the document deterministically: `$schema`
// first, then the remaining keys in sorted order, two-space indented. Key order
// is not semantic in JSON, and a stable order means a reinstall produces no
// diff — the property that makes this safe to run repeatedly.
func marshalRemoteMCPConfig(top map[string]json.RawMessage) ([]byte, error) {
	body, err := json.Marshal(orderedJSONObject(top))
	if err != nil {
		return nil, err
	}
	// Indented as a SECOND pass, not via MarshalIndent: the ordering above is a
	// json.Marshaler, and encoding/json compacts a Marshaler's output rather
	// than indenting it — which produced a valid but single-line config file.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		return nil, err
	}
	return append(pretty.Bytes(), '\n'), nil
}

// orderedJSONObject is a json.Marshaler over a raw-member map that emits keys in
// a stable order. encoding/json sorts map keys itself, but it cannot keep
// `$schema` first, and a config file whose schema line moves on every write
// looks like sctx mangled it.
type orderedJSONObject map[string]json.RawMessage

func (o orderedJSONObject) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "$schema") != (keys[j] == "$schema") {
			return keys[i] == "$schema"
		}
		return keys[i] < keys[j]
	})
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		b.Write(o[k])
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}
