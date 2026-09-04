package agentsetup

import (
	"github.com/synapctx/sctx/pkg/agentdoc"

	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexMCPInstallPreservesConfigAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	write(t, path, "model = \"gpt-test\"\n\n[projects.\"/work\"]\ntrust_level = \"trusted\"\n")
	tokens := map[string]string{
		"synapctx":   "sctx_live_syn",
		"cloudresty": "sctx_live_cloud",
	}

	changed, err := InstallCodexMCP(home, "https://mcp.synapctx.com", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || !strings.Contains(changed[0], "2 SynapCTX MCP server") {
		t.Fatalf("unexpected changes: %v", changed)
	}
	if strings.Contains(strings.Join(changed, " "), "sctx_live_") {
		t.Fatalf("change report leaked a credential: %v", changed)
	}
	got := read(t, path)
	if !strings.HasPrefix(got, "model = \"gpt-test\"\n\n[projects.\"/work\"]\ntrust_level = \"trusted\"\n") {
		t.Errorf("bytes before the managed block changed:\n%s", got)
	}
	for _, preserved := range []string{"model = \"gpt-test\"", "[projects.\"/work\"]", "trust_level = \"trusted\""} {
		if !strings.Contains(got, preserved) {
			t.Errorf("existing config %q was not preserved:\n%s", preserved, got)
		}
	}
	if strings.Index(got, "synapctx-cloudresty") > strings.Index(got, "synapctx-synapctx") {
		t.Errorf("registrations are not deterministic:\n%s", got)
	}
	for _, want := range []string{
		"[mcp_servers.synapctx-cloudresty]",
		"[mcp_servers.synapctx-synapctx]",
		"url = \"https://mcp.synapctx.com/mcp\"",
		"Authorization = \"Bearer sctx_live_cloud\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}

	st, err := InspectCodexMCP(home, "https://mcp.synapctx.com", tokens)
	if err != nil || !st.Complete() {
		t.Fatalf("installed config not complete: status=%+v err=%v", st, err)
	}
	changed, err = InstallCodexMCP(home, "https://mcp.synapctx.com", tokens)
	if err != nil || len(changed) != 0 {
		t.Fatalf("second install changed %v, err=%v", changed, err)
	}
}

func TestCodexMCPInstallUpdatesOwnedCredentialsOnly(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	write(t, path, "approval_policy = \"on-request\"\n")
	if _, err := InstallCodexMCP(home, "https://old.example", map[string]string{"acme": "sctx_live_old"}); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallCodexMCP(home, "https://new.example/", map[string]string{"acme": "sctx_live_new"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || !strings.HasPrefix(changed[0], "updated ") {
		t.Fatalf("unexpected changes: %v", changed)
	}
	got := read(t, path)
	if strings.Contains(got, "sctx_live_old") || strings.Contains(got, "old.example") {
		t.Errorf("old managed values survived:\n%s", got)
	}
	if !strings.Contains(got, "sctx_live_new") || !strings.Contains(got, "https://new.example/mcp") {
		t.Errorf("new managed values missing:\n%s", got)
	}
	if n := strings.Count(got, codexMCPBegin); n != 1 {
		t.Errorf("managed block appears %d times", n)
	}
	if !strings.HasPrefix(got, "approval_policy = \"on-request\"") {
		t.Errorf("user config changed:\n%s", got)
	}
}

func TestCodexMCPInstallRefusesUnmanagedNameCollision(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	original := "[mcp_servers.\"synapctx-acme\"]\nurl = \"https://someone-else.invalid/mcp\"\n"
	write(t, path, original)

	st, err := InspectCodexMCP(home, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Conflicts) != 1 || st.Conflicts[0] != "synapctx-acme" || st.Complete() {
		t.Fatalf("collision not reported: %+v", st)
	}
	if _, err := InstallCodexMCP(home, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_x"}); err == nil {
		t.Fatal("install overwrote or duplicated an unmanaged registration")
	}
	if got := read(t, path); got != original {
		t.Errorf("conflicting config changed:\n%s", got)
	}
}

func TestCodexMCPInstallRefusesBrokenManagedMarkers(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	original := "model = \"x\"\n" + codexMCPBegin + "\n[mcp_servers.synapctx-acme]\n"
	write(t, path, original)
	if _, err := InstallCodexMCP(home, "https://mcp.synapctx.com", map[string]string{"acme": "sctx_live_x"}); err == nil {
		t.Fatal("install guessed at a partial managed block")
	}
	if got := read(t, path); got != original {
		t.Errorf("broken config changed:\n%s", got)
	}
}

func TestCodexMCPStatusParticipatesInOverallCompleteness(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "codex")
	tokens := map[string]string{"acme": "sctx_live_x"}
	if _, err := Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}

	st, err := InspectWithCodexMCP(home, tokens, "https://mcp.synapctx.com")
	if err != nil {
		t.Fatal(err)
	}
	if st.Complete() {
		t.Fatal("current instructions hid the missing Codex MCP registration")
	}
	if len(st.Targets) != 1 || st.Targets[0].CodexMCP == nil || st.Targets[0].CodexMCP.Complete() {
		t.Fatalf("missing MCP state not attached: %+v", st.Targets)
	}

	if _, err := InstallCodexMCP(home, "https://mcp.synapctx.com", tokens); err != nil {
		t.Fatal(err)
	}
	st, err = InspectWithCodexMCP(home, tokens, "https://mcp.synapctx.com")
	if err != nil || !st.Complete() {
		t.Fatalf("fully configured Codex not complete: status=%+v err=%v", st, err)
	}
}

func TestCodexMCPNoCredentialsMeansNoRegistrationRequired(t *testing.T) {
	home := t.TempDir()
	configure(t, home, "codex")
	if _, err := Install(home, nil, agentdoc.SctxDoc); err != nil {
		t.Fatal(err)
	}
	st, err := InspectWithCodexMCP(home, nil, "https://mcp.synapctx.com", agentdoc.SctxDoc)
	if err != nil || !st.Complete() {
		t.Fatalf("standalone sctx install should not require MCP: status=%+v err=%v", st, err)
	}
}
