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
	home := t.TempDir()
	t.Setenv("HOME", home)
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
