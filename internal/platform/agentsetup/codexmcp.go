package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	codexMCPBegin = "# BEGIN SYNAPCTX MCP - managed by `sctx setup`; edits inside are replaced"
	codexMCPEnd   = "# END SYNAPCTX MCP"
)

// CodexMCPStatus is the capability half of a Codex setup. Instructions can tell
// Codex when to use SynapCTX, but only these registrations make the tools exist.
type CodexMCPStatus struct {
	ConfigPath string
	Servers    []string
	Installed  bool
	Stale      bool
	Conflicts  []string
}

// Complete means every desired registration is present in the exact block sctx
// would write, with no duplicate table elsewhere in the user's configuration.
func (s CodexMCPStatus) Complete() bool {
	return len(s.Servers) == 0 || (s.Installed && !s.Stale && len(s.Conflicts) == 0)
}

// InspectCodexMCP compares ~/.codex/config.toml with the registrations required
// by the configured organization credentials. Token values are compared in
// memory and never included in status or error output.
func InspectCodexMCP(home, endpoint string, orgTokens map[string]string) (CodexMCPStatus, error) {
	if home == "" {
		return CodexMCPStatus{}, errors.New("no home directory")
	}
	st := CodexMCPStatus{
		ConfigPath: filepath.Join(home, ".codex", "config.toml"),
		Servers:    codexMCPServerNames(orgTokens),
	}
	if len(st.Servers) == 0 {
		return st, nil
	}
	raw, err := os.ReadFile(st.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return CodexMCPStatus{}, fmt.Errorf("reading %s: %w", st.ConfigPath, err)
	}
	prefix, body, suffix, found, err := splitCodexMCPBlock(string(raw))
	if err != nil {
		return CodexMCPStatus{}, fmt.Errorf("inspecting %s: %w", st.ConfigPath, err)
	}
	st.Conflicts = conflictingCodexMCPServers(prefix+"\n"+suffix, st.Servers)
	st.Installed = found
	if found {
		st.Stale = strings.TrimSpace(body) != strings.TrimSpace(renderCodexMCPBody(endpoint, orgTokens))
	}
	return st, nil
}

// InstallCodexMCP installs or refreshes the registrations sctx owns. It never
// edits an unmanaged registration with the same name: that configuration may
// carry policy the user chose, and appending ours would create invalid TOML.
func InstallCodexMCP(home, endpoint string, orgTokens map[string]string) ([]string, error) {
	st, err := InspectCodexMCP(home, endpoint, orgTokens)
	if err != nil {
		return nil, err
	}
	if len(st.Servers) == 0 || st.Complete() {
		return nil, nil
	}
	if len(st.Conflicts) > 0 {
		return nil, fmt.Errorf("%s contains unmanaged MCP registration(s) for %s; left unchanged",
			st.ConfigPath, strings.Join(st.Conflicts, ", "))
	}

	raw, readErr := os.ReadFile(st.ConfigPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("reading %s: %w", st.ConfigPath, readErr)
	}
	prefix, _, suffix, found, splitErr := splitCodexMCPBlock(string(raw))
	if splitErr != nil {
		return nil, fmt.Errorf("updating %s: %w", st.ConfigPath, splitErr)
	}
	block := codexMCPBegin + "\n" + renderCodexMCPBody(endpoint, orgTokens) + codexMCPEnd + "\n"
	var out string
	if found {
		out = prefix + block + suffix
	} else {
		out = string(raw)
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" {
			out += "\n"
		}
		out += block
	}
	if err := os.MkdirAll(filepath.Dir(st.ConfigPath), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(st.ConfigPath), err)
	}
	if err := writePrivateFile(st.ConfigPath, []byte(out)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", st.ConfigPath, err)
	}
	verb := "registered"
	if st.Installed {
		verb = "updated"
	}
	return []string{fmt.Sprintf("%s %d SynapCTX MCP server(s) for OpenAI Codex (%s)", verb, len(st.Servers), st.ConfigPath)}, nil
}

func codexMCPServerNames(orgTokens map[string]string) []string {
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

func renderCodexMCPBody(endpoint string, orgTokens map[string]string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if !strings.HasSuffix(endpoint, "/mcp") {
		endpoint += "/mcp"
	}
	orgs := make([]string, 0, len(orgTokens))
	for org, token := range orgTokens {
		if strings.TrimSpace(org) != "" && strings.TrimSpace(token) != "" {
			orgs = append(orgs, strings.TrimSpace(org))
		}
	}
	sort.Strings(orgs)

	var b strings.Builder
	b.WriteString("# Contains API credentials. Keep this file private.\n")
	for _, org := range orgs {
		name := "synapctx-" + org
		fmt.Fprintf(&b, "[%s]\n", "mcp_servers."+name)
		fmt.Fprintf(&b, "url = %s\n", tomlBasicString(endpoint))
		fmt.Fprintf(&b, "http_headers = { Authorization = %s }\n\n", tomlBasicString("Bearer "+orgTokens[org]))
	}
	return b.String()
}

func tomlBasicString(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\b", "\\b",
		"\t", "\\t",
		"\n", "\\n",
		"\f", "\\f",
		"\r", "\\r",
	)
	return "\"" + replacer.Replace(value) + "\""
}

// splitCodexMCPBlock returns the text outside and inside our managed markers.
// Partial or repeated markers are refused: guessing at their extent risks
// deleting user configuration or leaving duplicate tables.
func splitCodexMCPBlock(in string) (prefix, body, suffix string, found bool, err error) {
	return splitManagedBlock(in, codexMCPBegin, codexMCPEnd)
}

// splitManagedBlock is the same operation for any pair of markers in this file:
// the MCP registrations and, since 2026-08-18, the auto-wrap hook.
func splitManagedBlock(in, beginMarker, endMarker string) (prefix, body, suffix string, found bool, err error) {
	codexMCPBegin, codexMCPEnd := beginMarker, endMarker
	starts := strings.Count(in, codexMCPBegin)
	ends := strings.Count(in, codexMCPEnd)
	if starts == 0 && ends == 0 {
		return in, "", "", false, nil
	}
	if starts != 1 || ends != 1 {
		return "", "", "", false, errors.New("managed SynapCTX MCP markers are incomplete or repeated")
	}
	start := strings.Index(in, codexMCPBegin)
	end := strings.Index(in, codexMCPEnd)
	if end < start {
		return "", "", "", false, errors.New("managed SynapCTX MCP markers are out of order")
	}
	bodyStart := start + len(codexMCPBegin)
	body = strings.TrimPrefix(in[bodyStart:end], "\n")
	suffixStart := end + len(codexMCPEnd)
	suffix = strings.TrimPrefix(in[suffixStart:], "\n")
	return in[:start], body, suffix, true, nil
}

func conflictingCodexMCPServers(outside string, desired []string) []string {
	wanted := make(map[string]bool, len(desired))
	for _, name := range desired {
		wanted[name] = true
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(outside, "\n") {
		name, ok := codexMCPTableServer(strings.TrimSpace(line))
		if ok && wanted[name] {
			seen[name] = true
		}
	}
	conflicts := make([]string, 0, len(seen))
	for name := range seen {
		conflicts = append(conflicts, name)
	}
	sort.Strings(conflicts)
	return conflicts
}

// codexMCPTableServer recognizes both bare and quoted server names, including
// nested tables such as [mcp_servers."synapctx-acme".http_headers].
func codexMCPTableServer(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	rest, ok := strings.CutPrefix(inner, "mcp_servers.")
	if !ok || rest == "" {
		return "", false
	}
	if rest[0] == '\'' || rest[0] == '"' {
		quote := rest[0]
		if end := strings.IndexByte(rest[1:], quote); end >= 0 {
			return rest[1 : end+1], true
		}
		return "", false
	}
	if dot := strings.IndexByte(rest, '.'); dot >= 0 {
		rest = rest[:dot]
	}
	return rest, rest != ""
}

func writePrivateFile(path string, body []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
