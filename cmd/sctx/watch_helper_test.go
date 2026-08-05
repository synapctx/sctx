package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
)

// The daemon lives in a SEPARATE BINARY so this module's go.mod stays free of
// private modules — sctx is public and must be buildable by strangers. These pin
// the seam, because a helper that cannot be found, or found in the wrong order,
// fails in a way that looks like the feature simply not working.

func TestAnExplicitHelperOverrideMustExist(t *testing.T) {
	t.Setenv("SCT__WATCH_HELPER", filepath.Join(t.TempDir(), "absent"))

	if _, err := findHelper(); err == nil {
		t.Fatal("a helper path that is not there must be an error, not a fallback")
	}
	// Silently falling back would run a DIFFERENT binary than the one named,
	// which is the worst outcome: it works, but not as configured.
	if _, err := findHelper(); !strings.Contains(err.Error(), "SCT__WATCH_HELPER") {
		t.Fatalf("error should name the override that failed, got %v", err)
	}
}

func TestAnExplicitHelperOverrideIsUsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sctxd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCT__WATCH_HELPER", path)

	got, err := findHelper()
	if err != nil {
		t.Fatalf("finding helper: %v", err)
	}
	if got != path {
		t.Fatalf("helper %q, want the override %q", got, path)
	}
}

func TestAMissingHelperIsReportedNotGuessed(t *testing.T) {
	t.Setenv("SCT__WATCH_HELPER", "")
	t.Setenv("PATH", t.TempDir())

	if _, err := findHelper(); err == nil {
		t.Skip("a sctxd exists beside the test binary; nothing to assert")
	} else if !strings.Contains(err.Error(), helperBinary) {
		t.Fatalf("error should name the binary it looked for, got %v", err)
	}
}

func TestStartupCarriesEveryOrgKeyAndNeverArgv(t *testing.T) {
	// A developer watches repositories across several organizations and holds one
	// key EACH. Passing a single token would serve one org and silently do
	// nothing for the rest.
	cfg := config.Config{
		WorkspaceProxyURL: "https://mcp.example.com/",
		ConfigFilePath:    "/home/dev/.config/sctx/config.toml",
		DefaultOrg:        "acme",
		OrgTokens: map[string]string{
			"acme":  "sctx_live_acme",
			"other": "sctx_live_other",
		},
	}

	raw, err := json.Marshal(watchStartup(cfg, []string{"/src"}))
	if err != nil {
		t.Fatalf("marshalling startup: %v", err)
	}
	var got struct {
		ProxyURL   string            `json:"proxyUrl"`
		Roots      []string          `json:"roots"`
		StateDir   string            `json:"stateDir"`
		OrgTokens  map[string]string `json:"orgTokens"`
		DefaultOrg string            `json:"defaultOrg"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decoding startup: %v", err)
	}

	if len(got.OrgTokens) != 2 || got.OrgTokens["other"] != "sctx_live_other" {
		t.Fatalf("startup carried %v, want a key per organization", got.OrgTokens)
	}
	if got.DefaultOrg != "acme" {
		t.Fatalf("defaultOrg %q, want acme", got.DefaultOrg)
	}
	// Trailing slash stripped once, here, rather than in the helper: config
	// resolution lives in ONE place or the two disagree.
	if got.ProxyURL != "https://mcp.example.com" {
		t.Fatalf("proxyUrl %q, want it normalised", got.ProxyURL)
	}
	if got.StateDir != "/home/dev/.config/sctx/workspace" {
		t.Fatalf("stateDir %q, want it beside the config file", got.StateDir)
	}
}

func TestASingleKeyConfigurationStillResolves(t *testing.T) {
	// An env override or a pre-multi-org config file has no [org.*] sections. Those
	// installs must keep working, or upgrading sctx silently disables watch.
	cfg := config.Config{
		WorkspaceProxyURL: "https://mcp.example.com",
		ConfigFilePath:    "/tmp/config.toml",
		DefaultOrg:        "acme",
		TelemetryTokenEnv: "sctx_live_env",
	}

	raw, _ := json.Marshal(watchStartup(cfg, []string{"/src"}))
	var got struct {
		OrgTokens map[string]string `json:"orgTokens"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.OrgTokens["acme"] != "sctx_live_env" {
		t.Fatalf("single-key config produced %v", got.OrgTokens)
	}
}
