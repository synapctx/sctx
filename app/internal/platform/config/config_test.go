package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHome points $HOME at a fresh temp dir for the duration of the test so
// Load's ~/.config/sctx/config.toml resolution is isolated and repeatable.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// clearTelemetryEnv ensures no ambient SCT__* value leaks in from the host
// environment and shadows a test's env-layer expectations.
func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SCT__TELEMETRY_ENDPOINT", "SCT__TELEMETRY_TOKEN", "SCT__TELEMETRY_ENABLED",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func writeConfigTOML(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "sctx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestLoadPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		fileBody     string
		env          map[string]string
		wantEndpoint string
		wantToken    string
	}{
		{
			name:         "built-in default when neither env nor file set",
			wantEndpoint: "http://127.0.0.1:6221/v1/telemetry/exec",
			wantToken:    "",
		},
		{
			name:         "file overrides built-in default",
			fileBody:     `telemetry_endpoint = "https://sctx.synapctx.com/v1/telemetry/exec"` + "\n" + `telemetry_token = "sctx_live_fromfile"` + "\n",
			wantEndpoint: "https://sctx.synapctx.com/v1/telemetry/exec",
			wantToken:    "sctx_live_fromfile",
		},
		{
			name:         "env overrides file",
			fileBody:     `telemetry_endpoint = "https://sctx.synapctx.com/v1/telemetry/exec"` + "\n" + `telemetry_token = "sctx_live_fromfile"` + "\n",
			env:          map[string]string{"SCT__TELEMETRY_ENDPOINT": "https://env.example/v1/telemetry/exec", "SCT__TELEMETRY_TOKEN": "sctx_live_fromenv"},
			wantEndpoint: "https://env.example/v1/telemetry/exec",
			wantToken:    "sctx_live_fromenv",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := withHome(t)
			clearTelemetryEnv(t)
			if tc.fileBody != "" {
				writeConfigTOML(t, home, tc.fileBody)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TelemetryEndpoint != tc.wantEndpoint {
				t.Errorf("TelemetryEndpoint = %q, want %q", cfg.TelemetryEndpoint, tc.wantEndpoint)
			}
			if token, _ := cfg.TokenForOrg(""); token != tc.wantToken {
				t.Errorf("TokenForOrg(\"\") = %q, want %q", token, tc.wantToken)
			}
			wantConfigPath := filepath.Join(home, ".config", "sctx", "config.toml")
			if cfg.ConfigFilePath != wantConfigPath {
				t.Errorf("ConfigFilePath = %q, want %q", cfg.ConfigFilePath, wantConfigPath)
			}
		})
	}
}

func TestLoadMalformedConfigFileWarnsAndContinuesEnvOnly(t *testing.T) {
	home := withHome(t)
	clearTelemetryEnv(t)
	// Malformed: unquoted value violates the strict key = "value" grammar.
	writeConfigTOML(t, home, "telemetry_endpoint = not-quoted\n")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w

	cfg, loadErr := Load()

	os.Stderr = origStderr
	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r) //nolint:errcheck
	stderr := buf.String()

	if loadErr != nil {
		t.Fatalf("Load must never fail on a malformed config file, got: %v", loadErr)
	}
	if cfg.TelemetryEndpoint != "http://127.0.0.1:6221/v1/telemetry/exec" {
		t.Errorf("TelemetryEndpoint = %q, want the built-in default after a malformed file", cfg.TelemetryEndpoint)
	}
	if got := strings.Count(stderr, "sctx: warning:"); got != 1 {
		t.Errorf("expected exactly one warning line, got %d in: %q", got, stderr)
	}
}

func TestLoadMissingConfigFileIsSilent(t *testing.T) {
	withHome(t)
	clearTelemetryEnv(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w

	_, loadErr := Load()

	os.Stderr = origStderr
	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r) //nolint:errcheck

	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no stderr output for a missing config file, got: %q", buf.String())
	}
}

func TestTokenForOrgSectionedConfig(t *testing.T) {
	home := withHome(t)
	clearTelemetryEnv(t)
	writeConfigTOML(t, home, ""+
		`telemetry_endpoint = "https://sctx.synapctx.com/v1/telemetry/exec"`+"\n"+
		`default_org = "synapctx"`+"\n"+
		"\n"+
		`[org.synapctx]`+"\n"+
		`token = "sctx_live_synapctx"`+"\n"+
		"\n"+
		`[org.parlitrack]`+"\n"+
		`token = "sctx_live_parlitrack"`+"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultOrg != "synapctx" {
		t.Errorf("DefaultOrg = %q, want synapctx", cfg.DefaultOrg)
	}
	if token, ok := cfg.TokenForOrg("parlitrack"); !ok || token != "sctx_live_parlitrack" {
		t.Errorf("TokenForOrg(parlitrack) = (%q, %v), want (sctx_live_parlitrack, true)", token, ok)
	}
	if token, ok := cfg.TokenForOrg("synapctx"); !ok || token != "sctx_live_synapctx" {
		t.Errorf("TokenForOrg(synapctx) = (%q, %v), want (sctx_live_synapctx, true)", token, ok)
	}
	// Empty org (event outside any git repo) falls back to the default org.
	if token, ok := cfg.TokenForOrg(""); !ok || token != "sctx_live_synapctx" {
		t.Errorf("TokenForOrg(\"\") = (%q, %v), want (sctx_live_synapctx, true)", token, ok)
	}
	// Unknown org with sectioned config present: no key yet, retain-don't-deliver.
	if token, ok := cfg.TokenForOrg("cloudresty"); ok || token != "" {
		t.Errorf("TokenForOrg(cloudresty) = (%q, %v), want (\"\", false)", token, ok)
	}
}

// TestTokenForOrgNoAuthDeliversUnauthenticated locks in the default local-dev
// topology: with no env token, no legacy token, and no [org.*] sections, the
// resolver must return ("", true) so flush POSTs to the trusted local proxy
// without a bearer — NOT ("", false), which would retain every event forever.
func TestTokenForOrgNoAuthDeliversUnauthenticated(t *testing.T) {
	withHome(t)
	clearTelemetryEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HasAnyToken() {
		t.Fatalf("HasAnyToken() = true, want false for a bare default config")
	}
	for _, org := range []string{"", "synapctx", "parlitrack"} {
		if token, ok := cfg.TokenForOrg(org); !ok || token != "" {
			t.Errorf("TokenForOrg(%q) = (%q, %v), want (\"\", true) for unauthenticated local delivery", org, token, ok)
		}
	}
}

func TestTokenForOrgEnvOverridesAllOrgs(t *testing.T) {
	home := withHome(t)
	clearTelemetryEnv(t)
	writeConfigTOML(t, home, ""+
		`default_org = "synapctx"`+"\n"+
		"\n"+
		`[org.synapctx]`+"\n"+
		`token = "sctx_live_synapctx"`+"\n")
	t.Setenv("SCT__TELEMETRY_TOKEN", "sctx_live_envoverride")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, org := range []string{"synapctx", "parlitrack", ""} {
		if token, ok := cfg.TokenForOrg(org); !ok || token != "sctx_live_envoverride" {
			t.Errorf("TokenForOrg(%q) = (%q, %v), want (sctx_live_envoverride, true)", org, token, ok)
		}
	}
}

func TestTokenForOrgLegacyBareToken(t *testing.T) {
	home := withHome(t)
	clearTelemetryEnv(t)
	writeConfigTOML(t, home, `telemetry_token = "sctx_live_legacy"`+"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, org := range []string{"synapctx", "parlitrack", ""} {
		if token, ok := cfg.TokenForOrg(org); !ok || token != "sctx_live_legacy" {
			t.Errorf("TokenForOrg(%q) = (%q, %v), want (sctx_live_legacy, true)", org, token, ok)
		}
	}
}

func TestHasAnyToken(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"nothing configured", Config{}, false},
		{"env override", Config{TelemetryTokenEnv: "x"}, true},
		{"legacy token", Config{LegacyToken: "x"}, true},
		{"sectioned org tokens", Config{OrgTokens: map[string]string{"synapctx": "x"}}, true},
	}
	for _, tc := range cases {
		if got := tc.cfg.HasAnyToken(); got != tc.want {
			t.Errorf("%s: HasAnyToken() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
