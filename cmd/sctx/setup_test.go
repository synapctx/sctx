package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/agentsetup"
	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/pkg/agentdoc"
)

// withAgent makes a home where one agent is configured but untaught.
func withAgent(t *testing.T) agentsetup.Status {
	t.Helper()
	home := t.TempDir()
	a, _ := agentdoc.AgentByID("claude")
	if err := os.MkdirAll(filepath.Join(home, a.Detect[0]), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := agentsetup.Inspect(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Detected() || st.Complete() {
		t.Fatalf("fixture must be detected-but-incomplete, got %+v", st)
	}
	return st
}

func emptyHome(t *testing.T) agentsetup.Status {
	t.Helper()
	st, err := agentsetup.Inspect(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func completeStatus(t *testing.T) agentsetup.Status {
	t.Helper()
	home := t.TempDir()
	a, _ := agentdoc.AgentByID("claude")
	if err := os.MkdirAll(filepath.Join(home, a.Detect[0]), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := agentsetup.Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	st, err := agentsetup.Inspect(home, []string{"acme"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Complete() {
		t.Fatal("fixture is not complete")
	}
	return st
}

// THE load-bearing test. sctx's output is consumed by an agent on every wrapped
// command; a setup notice in that stream is a token cost on a product sold on
// token savings, and something the agent may act on mid-task. Non-interactive
// must mean silent, unconditionally — including when setup really is broken.
func TestTheNudgeNeverFiresWhenNobodyIsWatching(t *testing.T) {
	broken := withAgent(t)
	if shouldNudge(broken, false, "") {
		t.Error("nudged on a non-terminal stderr — that output goes into an agent's context")
	}
	if !shouldNudge(broken, true, "") {
		t.Error("did not nudge a human on a broken setup, which is the entire point")
	}
}

func TestTheNudgeIsSuppressibleAndSilentWhenSetupIsFine(t *testing.T) {
	if shouldNudge(withAgent(t), true, "1") {
		t.Error("SCT__NO_SETUP_NUDGE did not suppress it")
	}
	if shouldNudge(completeStatus(t), true, "") {
		t.Error("nudged a machine that is already set up")
	}
}

// SYNAPCTX.md describes MCP tools that need an API key. Offering it before one
// exists produces failed calls and teaches the agent the file is unreliable.
func TestSynapctxIsOnlyOfferedOnceAKeyExists(t *testing.T) {
	if got := docsFor(config.Config{}); len(got) != 1 || got[0].Name != "SCTX.md" {
		t.Errorf("unauthenticated machine offered %v, want SCTX.md alone", names(got))
	}
	withKey := config.Config{OrgTokens: map[string]string{"acme": "sctx_live_x"}}
	if got := docsFor(withKey); len(got) != 2 {
		t.Errorf("authenticated machine offered %v, want both", names(got))
	}
}

func TestCodexCredentialsKeepPerOrgKeysDespiteTelemetryOverride(t *testing.T) {
	cfg := config.Config{
		OrgTokens: map[string]string{
			"acme":  "sctx_live_acme",
			"other": "sctx_live_other",
		},
		TelemetryTokenEnv: "sctx_live_temporary_override",
	}
	got := codexOrgTokens(cfg)
	if got["acme"] != "sctx_live_acme" || got["other"] != "sctx_live_other" {
		t.Fatalf("telemetry override replaced org-scoped MCP credentials: %v", got)
	}
}

func TestLegacyCredentialNeedsKnownOrgForCodex(t *testing.T) {
	unknown := config.Config{LegacyToken: "sctx_live_legacy"}
	if got := codexOrgTokens(unknown); len(got) != 0 {
		t.Fatalf("invented an organization for a legacy key: %v", got)
	}
	known := config.Config{LegacyToken: "sctx_live_legacy", DefaultOrg: "acme"}
	if got := codexOrgTokens(known); got["acme"] != "sctx_live_legacy" {
		t.Fatalf("did not adopt a legacy key with a known org: %v", got)
	}
}

func names(docs []agentdoc.Doc) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Name)
	}
	return out
}

// A machine with no agent must NOT be nudged on the wrapped path: we would be
// telling someone to fix something we cannot see, on every command, forever.
// `sctx setup` and `sctx gain` say it when asked, which is the right volume.
func TestAnUndetectedMachineIsNotNudgedOnEveryCommand(t *testing.T) {
	if shouldNudge(emptyHome(t), true, "") {
		t.Error("nudged a machine where no agent was detected")
	}
}

// `sctx gain` is the one routine command a human reads on purpose, so it says
// this unconditionally — including the undetected case, which the silent nudge
// deliberately skips. A savings report that omits "your agent was never told any
// of this exists" is not an honest report.
func TestGainNoticeSpeaksWhereTheNudgeStaysQuiet(t *testing.T) {
	if got := gainNotice(emptyHome(t)); !strings.Contains(got, "No coding agent detected") {
		t.Errorf("gain must report an undetected machine, got %q", got)
	}
	if got := gainNotice(withAgent(t)); !strings.Contains(got, "sctx setup --install") {
		t.Errorf("gain must name the fix, got %q", got)
	}
	if got := gainNotice(completeStatus(t)); got != "" {
		t.Errorf("gain must stay silent when setup is fine, got %q", got)
	}
}

// Status output must distinguish "we found nothing" from "we found something
// broken", and must say WHAT it looked for — otherwise "none detected" is an
// accusation rather than an instruction.
func TestStatusForAnUndetectedMachineSaysWhatItLookedFor(t *testing.T) {
	var buf bytes.Buffer
	printSetupStatus(&buf, emptyHome(t), config.Config{}, false)
	out := buf.String()
	if !strings.Contains(out, "none detected") || !strings.Contains(out, "nothing was created") {
		t.Errorf("must say nothing was found AND nothing was written:\n%s", out)
	}
	if !strings.Contains(out, ".claude") || !strings.Contains(out, ".codex") {
		t.Errorf("must list the paths consulted:\n%s", out)
	}
	if !strings.Contains(out, "--list-agents") {
		t.Errorf("must offer the escape hatch for an agent we do not detect:\n%s", out)
	}
}

// This is the regression the feature exists for: before this test, setup could
// write current Codex instructions, return success, and leave `codex mcp list`
// empty. Exercise the command boundary, not only the config writer.
func TestSetupInstallGivesCodexInstructionsAndMCPAbilityTogether(t *testing.T) {
	stubMCPProbe(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows: os.UserHomeDir reads USERPROFILE, not HOME
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		OrgTokens:         map[string]string{"acme": "sctx_live_acme"},
		DefaultOrg:        "acme",
		WorkspaceProxyURL: "https://mcp.synapctx.com",
		SpoolDir:          filepath.Join(home, ".config", "sctx", "spool"),
	}
	if code := runSetup(cfg, []string{"--install"}); code != 0 {
		t.Fatalf("setup exit = %d, want 0", code)
	}
	st, err := agentsetup.InspectWithCodexMCP(home, codexOrgTokens(cfg), cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Complete() {
		t.Fatalf("setup returned success without complete Codex capability: %+v", st.Targets)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Error("Codex-only setup created Claude configuration")
	}
}

func TestStatusSeparatesCodexInstructionsFromMCPRegistration(t *testing.T) {
	home := t.TempDir()
	configure := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configure, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := agentsetup.Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{OrgTokens: map[string]string{"acme": "sctx_live_acme"}, WorkspaceProxyURL: "https://mcp.synapctx.com"}
	st, err := agentsetup.InspectWithCodexMCP(home, codexOrgTokens(cfg), cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printSetupStatus(&buf, st, cfg, false)
	out := buf.String()
	if !strings.Contains(out, "[ok]      OpenAI Codex CLI") {
		t.Errorf("current instructions were not reported independently:\n%s", out)
	}
	if !strings.Contains(out, "Codex MCP servers") || !strings.Contains(out, "[missing ]") {
		t.Errorf("missing MCP capability was not reported independently:\n%s", out)
	}
	if !strings.Contains(out, "sidecar files where includes are supported") ||
		!strings.Contains(out, "otherwise inlined into the agent's root instructions") {
		t.Errorf("instruction delivery did not distinguish sidecars from inline content:\n%s", out)
	}
}

func TestJoinAnd(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"A"}, "A"},
		{[]string{"A", "B"}, "A and B"},
		{[]string{"A", "B", "C"}, "A, B and C"},
	} {
		if got := joinAnd(tc.in); got != tc.want {
			t.Errorf("joinAnd(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The same regression as the Codex one above, for the agents that were still
// missing it: Kilo Code and OpenCode were taught a document whose whole subject
// is a set of MCP tools, while nothing ever registered a server for them. And
// for the clients whose registry sctx does NOT write, setup has to say so out
// loud — silence there reads as "registered", and the customer finds out when
// the agent calls a tool that does not exist.
func TestSetupInstallRegistersMCPForKiloAndSaysWhichClientsItCannot(t *testing.T) {
	stubMCPProbe(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows: os.UserHomeDir reads USERPROFILE, not HOME
	for _, dir := range []string{".config/kilo", ".gemini"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{
		OrgTokens:         map[string]string{"acme": "sctx_live_acme"},
		DefaultOrg:        "acme",
		WorkspaceProxyURL: "https://mcp.synapctx.com",
		SpoolDir:          filepath.Join(home, ".config", "sctx", "spool"),
	}
	if code := runSetup(cfg, []string{"--install"}); code != 0 {
		t.Fatalf("setup exit = %d, want 0", code)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".config", "kilo", "kilo.json"))
	if err != nil {
		t.Fatalf("Kilo was taught but never registered: %v", err)
	}
	if !strings.Contains(string(raw), "synapctx-acme") {
		t.Errorf("no SynapCTX server in Kilo's config:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "kilo", "AGENTS.md")); err != nil {
		t.Errorf("Kilo instructions not written where 7.4+ reads them: %v", err)
	}

	st, err := agentsetup.InspectWithMCP(home, codexOrgTokens(cfg), cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	printSetupStatus(&buf, st, cfg, true)
	out := buf.String()
	if !strings.Contains(out, "Kilo Code MCP servers") || !strings.Contains(out, "[ok      ] synapctx-acme") {
		t.Errorf("Kilo's registration was not reported:\n%s", out)
	}
	// Gemini is registered too, in ITS spelling, and gets its own hook.
	gemini, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("Gemini was detected but never registered: %v", err)
	}
	if !strings.Contains(string(gemini), `"httpUrl"`) || !strings.Contains(string(gemini), "synapctx-acme") {
		t.Errorf("Gemini's registration is missing or in the wrong dialect:\n%s", gemini)
	}
	if !strings.Contains(string(gemini), "hook gemini") {
		t.Errorf("Gemini's auto-wrap hook was not wired:\n%s", gemini)
	}
	// And Kilo gets the plugin, since it has no hook system.
	plugin := filepath.Join(home, ".config", "kilo", "plugin", "sctx.js")
	if _, err := os.Stat(plugin); err != nil {
		t.Errorf("Kilo's auto-wrap plugin was not installed: %v", err)
	}
	if !strings.Contains(out, "command wrapping") {
		t.Errorf("the wrapping section is missing:\n%s", out)
	}
	for _, want := range []string{"Kilo Code", "plugin", "Gemini CLI", "hook"} {
		if !strings.Contains(out, want) {
			t.Errorf("wrapping status does not mention %q:\n%s", want, out)
		}
	}
}

// stubMCPProbe keeps setup's reachability check off the network. A test that
// dials a real host fails on an aeroplane and passes in CI for reasons that have
// nothing to do with the code.
func stubMCPProbe(t *testing.T) {
	t.Helper()
	original := probeMCPEndpoint
	probeMCPEndpoint = func(string, string) (probeResult, string) {
		return probeReachable, "responding, and your API key was accepted"
	}
	t.Cleanup(func() { probeMCPEndpoint = original })
}

// A REGISTRATION POINTING AT A HOST THAT IS NOT LISTENING MUST NOT REPORT OK.
//
// This is the failure the whole endpoint probe exists for: until `sctx init`
// persisted an MCP host, config.Load fell back to the local dev proxy, so an
// authenticated machine registered every agent against http://127.0.0.1:6220 —
// and setup called it "[ok] registered", because the text was in the file. The
// agent then showed every SynapCTX tool as failing to connect, with nothing in
// sctx admitting anything was wrong.
func TestSetupFailsWhenTheMCPHostIsNotListening(t *testing.T) {
	original := probeMCPEndpoint
	probeMCPEndpoint = func(string, string) (probeResult, string) { return probeUnreachable, "connection refused" }
	t.Cleanup(func() { probeMCPEndpoint = original })

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		OrgTokens:         map[string]string{"acme": "sctx_live_acme"},
		WorkspaceProxyURL: "http://127.0.0.1:6220",
	}
	if _, err := agentsetup.Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := agentsetup.InstallCodexMCP(home, cfg.WorkspaceProxyURL, cfg.OrgTokens); err != nil {
		t.Fatal(err)
	}
	st, err := agentsetup.InspectWithMCP(home, cfg.OrgTokens, cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if printMCPEndpointStatus(&buf, st, cfg) {
		t.Error("a dead MCP host was reported as fine")
	}
	out := buf.String()
	if !strings.Contains(out, "[unreachable]") || !strings.Contains(out, "connection refused") {
		t.Errorf("the reason was not reported:\n%s", out)
	}
	if !strings.Contains(out, "workspace_proxy_url") {
		t.Errorf("no remedy was offered:\n%s", out)
	}
}

// An operator who chose their own MCP host must keep it: writeConfigFile
// rewrites the file wholesale, so a value not threaded through is erased — and
// the erasure only shows up later, as agents quietly pointing somewhere else.
func TestConfigRewritePreservesAChosenMCPHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := writeConfigFile(path, defaultInitEndpoint, "https://mcp.internal.example", "acme",
		map[string]string{"acme": "sctx_live_acme"}, config.ConsentRecord{}, "", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `workspace_proxy_url = "https://mcp.internal.example"`) {
		t.Fatalf("the MCP host was not persisted:\n%s", raw)
	}
	loaded := config.Config{WorkspaceProxyURL: "https://mcp.internal.example"}
	if got := configuredWorkspaceProxy(loaded); got != "https://mcp.internal.example" {
		t.Errorf("a rewrite would drop it: %q", got)
	}
	// The shipped default is not a choice anyone made, so it must NOT be frozen
	// into the file: the endpoint is ours and may move, and a machine that never
	// chose a host has to follow the binary rather than a copy of today's value.
	if got := configuredWorkspaceProxy(config.Config{WorkspaceProxyURL: config.DefaultWorkspaceProxy}); got != "" {
		t.Errorf("the shipped default would be frozen into the config file: %q", got)
	}
}

// The argv-fingerprint salt must survive the same wholesale rewrite: it is
// generated once per machine by config.Load, and a rewrite that dropped it
// would make config.Load mint a NEW one on the next process, silently
// breaking the "same command as before" signal it exists to provide.
func TestConfigRewritePreservesTheArgvSalt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := writeConfigFile(path, defaultInitEndpoint, "", "acme",
		map[string]string{"acme": "sctx_live_acme"}, config.ConsentRecord{}, "deadbeef", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `argv_salt = "deadbeef"`) {
		t.Fatalf("the argv salt was not persisted:\n%s", raw)
	}
}

// The redact opt-in must survive the same wholesale rewrite an operator's
// argv_salt and MCP host do, and it must NOT appear in the file at all when
// off — the default this release — so a plain install's config.toml stays
// unchanged from before this feature existed.
func TestConfigRewriteThreadsRedact(t *testing.T) {
	dir := t.TempDir()

	onPath := filepath.Join(dir, "on.toml")
	if err := writeConfigFile(onPath, defaultInitEndpoint, "", "acme",
		map[string]string{"acme": "sctx_live_acme"}, config.ConsentRecord{}, "", true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(onPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `redact = "true"`) {
		t.Fatalf("redact=true was not persisted:\n%s", raw)
	}

	offPath := filepath.Join(dir, "off.toml")
	if err := writeConfigFile(offPath, defaultInitEndpoint, "", "acme",
		map[string]string{"acme": "sctx_live_acme"}, config.ConsentRecord{}, "", false); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(offPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "redact") {
		t.Fatalf("redact=false should be omitted, not written:\n%s", raw)
	}
}

// A CUSTOMER WHO INSTALLS SCTX AND PASTES A KEY MUST REACH OUR MCP HOST WITH NO
// FURTHER CONFIGURATION.
//
// The default was the local dev proxy, and `sctx init` never wrote a host, so
// every customer's agents were registered against a port on their own laptop.
// Nobody outside this repository has any way to know that endpoint exists.
func TestTheHostedMCPHostIsTheDefaultWithNoConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // windows: os.UserHomeDir reads USERPROFILE, not HOME
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	// Unset, not empty: an explicitly empty override is itself a choice, and
	// this test is about the machine that made none.
	if original, ok := os.LookupEnv("SCT__WORKSPACE_PROXY_URL"); ok {
		os.Unsetenv("SCT__WORKSPACE_PROXY_URL")
		t.Cleanup(func() { os.Setenv("SCT__WORKSPACE_PROXY_URL", original) })
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceProxyURL != "https://mcp.synapctx.com" {
		t.Errorf("a fresh install points at %q, want the hosted MCP host", cfg.WorkspaceProxyURL)
	}
	// And a private deployment still wins, or self-hosting is impossible.
	t.Setenv("SCT__WORKSPACE_PROXY_URL", "https://mcp.internal.example")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceProxyURL != "https://mcp.internal.example" {
		t.Errorf("an operator override was ignored: %q", cfg.WorkspaceProxyURL)
	}
}

// A HOST THAT IS UP AND A KEY THAT IS ACCEPTED ARE TWO DIFFERENT CLAIMS.
//
// The probe used to send no credential, so the host's correct 401 was printed as
// "[ok] responding (401 Unauthorized)" — alarming on a working install, and
// blind to the one thing it should have caught: a revoked key against a healthy
// host. Those two states must never print the same line again.
func TestSetupDistinguishesAHealthyHostFromARejectedKey(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		OrgTokens:         map[string]string{"acme": "sctx_live_acme"},
		DefaultOrg:        "acme",
		WorkspaceProxyURL: "https://mcp.synapctx.com",
	}
	if _, err := agentsetup.Install(home, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := agentsetup.InstallCodexMCP(home, cfg.WorkspaceProxyURL, cfg.OrgTokens); err != nil {
		t.Fatal(err)
	}
	st, err := agentsetup.InspectWithMCP(home, cfg.OrgTokens, cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		t.Fatal(err)
	}

	// The credential the probe offers has to be a real configured one, or it
	// proves nothing about this machine.
	org, token := probeCredential(cfg)
	if org != "acme" || token != "sctx_live_acme" {
		t.Errorf("probe would authenticate as %q/%q, want the configured key", org, token)
	}

	original := probeMCPEndpoint
	t.Cleanup(func() { probeMCPEndpoint = original })

	probeMCPEndpoint = func(_, tok string) (probeResult, string) {
		if tok == "" {
			t.Error("the probe sent no credential, so a 401 would be its own fault")
		}
		return probeReachable, "responding, and your API key was accepted"
	}
	var ok bytes.Buffer
	if !printMCPEndpointStatus(&ok, st, cfg) {
		t.Error("an accepted key was not reported as healthy")
	}
	if strings.Contains(ok.String(), "401") || !strings.Contains(ok.String(), "accepted") {
		t.Errorf("a working install still reads like a failure:\n%s", ok.String())
	}

	probeMCPEndpoint = func(string, string) (probeResult, string) { return probeRejected, "401 Unauthorized" }
	var rejected bytes.Buffer
	if printMCPEndpointStatus(&rejected, st, cfg) {
		t.Error("a rejected key was reported as fine")
	}
	out := rejected.String()
	if !strings.Contains(out, "refused the acme key") {
		t.Errorf("the rejection does not say whose key:\n%s", out)
	}
	if !strings.Contains(out, "sctx init") {
		t.Errorf("no remedy was offered:\n%s", out)
	}
}
