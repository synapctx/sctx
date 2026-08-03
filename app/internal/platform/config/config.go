// Package config binds SCT__ environment variables (naming conventions) to
// the CLI configuration. sctx's own knobs are env-over-file: environment
// variables always win, falling back to ~/.config/sctx/config.toml (written
// by `sctx init`), falling back to a built-in default. Either layer can be
// absent; the environment is what can never collide with the wrapped
// command's flags, and it always wins, so that invariant holds regardless
// of the file.
package config

import (
	"fmt"
	"github.com/synapctx/sctx/internal/domain/telemetry"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	env "github.com/cloudresty/go-env"
)

// defaultLocalEndpoint is where telemetry goes before `sctx init` has written a
// config: the local developer-mcp-proxy's TELEMETRY INGEST listener.
//
// It was :6220 (the MCP port) until 2026-07-27. That route was removed from
// :6220 some time earlier — it had answered an UNAUTHENTICATED POST on the
// public MCP host — so a fresh install with no config was posting into a dead
// path. The spool retains on non-2xx, so nothing was lost, but savings silently
// never arrived and the only symptom was a `sctx gain` that stayed at zero.
const defaultLocalEndpoint = "http://127.0.0.1:6221/v1/telemetry/exec"

// defaultWorkspaceProxy is where the workspace daemon pushes before anything is
// configured: the local dev proxy, matching defaultLocalEndpoint. `sctx init`
// writes the real host, so a developer who has authenticated never sees this.
const defaultWorkspaceProxy = "http://127.0.0.1:6220"

type Config struct {
	ApplicationName        string
	ApplicationEnvironment string
	DebugLoggingEnabled    bool
	TelemetryEnabled       bool
	TelemetryEndpoint      string
	// WorkspaceProxyURL is the developer-mcp-proxy base URL the workspace daemon
	// pushes uncommitted-code deltas to.
	//
	// SEPARATE from TelemetryEndpoint because they are different hosts: telemetry
	// is ingested on the CLI host (sctx.*) and the workspace routes are served on
	// the MCP host (mcp.*). Deriving one from the other by rewriting a label would
	// work for our own deployment and break for anyone self-hosting under names we
	// did not predict.
	WorkspaceProxyURL string
	// OrgTokens maps a GitHub org slug -> its sctx_live_ API key, from the
	// [org.<slug>] sections of the config file. Nil/empty ⇒ no sectioned keys.
	OrgTokens map[string]string
	// DefaultOrg attributes empty-repository events (commands run outside any
	// git repo) to one org's key. From default_org in the file.
	DefaultOrg string
	// TelemetryTokenEnv is SCT__TELEMETRY_TOKEN: an explicit single-key
	// override that applies to EVERY org (server org-guards). Highest priority.
	TelemetryTokenEnv string
	// LegacyToken is a pre-multi-org bare `telemetry_token` in the file. Applies
	// to every org, but ONLY when no sectioned [org.*] keys exist. Back-compat.
	LegacyToken string
	// ServiceTelemetryEnabled covers the customer's OWN usage report — the
	// savings their console renders. Authorised by holding an API key rather than
	// by a prompt: an active key is a contract, and a token optimiser that cannot
	// report its savings is missing half its product. Delivery still requires a
	// token, which TokenForOrg enforces, so an unauthenticated machine sends
	// nothing regardless.
	ServiceTelemetryEnabled bool
	// ImprovementTelemetryEnabled covers what WE learn from — which commands sctx
	// fails to cover. Opt-in, because its value comes from aggregating across
	// customers and nobody agreed to that by buying a licence.
	ImprovementTelemetryEnabled bool
	// Consent is the customer's recorded answer about telemetry. TelemetryEnabled
	// is already resolved from it; this is carried so `sctx telemetry` can
	// explain WHY delivery is on or off, and so writing the config back does not
	// erase the record.
	Consent ConsentRecord
	// TelemetryExplicit reports that an explicit telemetry_enabled was set (file
	// or environment) and therefore decided the matter without consulting
	// consent. Kept so we never prompt someone who already answered by
	// configuration.
	TelemetryExplicit bool
	ForceTier         string // aggressive | relaxed | verbatim | off | ""
	MaxOutputBytes    int64
	StatsDBPath       string
	SpoolDir          string
	ConfigFilePath    string
}

// TokenForOrg returns the API key to deliver an event attributed to org
// (org is the slug prefix of repositoryName, "" for events outside a repo),
// and whether telemetry should be delivered for it at all.
func (c Config) TokenForOrg(org string) (string, bool) {
	if c.TelemetryTokenEnv != "" { // explicit env override: single key, all orgs
		return c.TelemetryTokenEnv, true
	}
	if len(c.OrgTokens) > 0 { // sectioned config
		key := org
		if key == "" {
			key = c.DefaultOrg
		}
		if key != "" {
			if t := c.OrgTokens[key]; t != "" {
				return t, true
			}
		}
		return "", false // no key for this org yet ⇒ retain, don't deliver
	}
	if c.LegacyToken != "" { // pre-multi-org bare token: single key, all orgs
		return c.LegacyToken, true
	}
	// Nothing is authenticated at all: this is the default local-dev
	// topology (endpoint is the trusted local proxy ingest port, which
	// accepts unauthenticated telemetry). Deliver with no bearer — ok=true so
	// flush POSTs rather than retaining forever. A misconfigured remote
	// endpoint simply 401s and the batch is retained, matching prior behavior.
	return "", true
}

// HasAnyToken reports whether any delivery key is configured (env, sectioned,
// or legacy). Used by doctor and telemetryMode.
func (c Config) HasAnyToken() bool {
	return c.TelemetryTokenEnv != "" || c.LegacyToken != "" || len(c.OrgTokens) > 0
}

func Load() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolving home directory: %w", err)
	}
	base := filepath.Join(home, ".config", "sctx")
	configPath := filepath.Join(base, "config.toml")
	fv := loadConfigFile(configPath)

	cfg := Config{
		ApplicationName:        env.Get("SCT__APPLICATION_NAME", "sctx"),
		ApplicationEnvironment: env.Get("SCT__APPLICATION_ENVIRONMENT", "development"),
		DebugLoggingEnabled:    env.Get("SCT__APPLICATION_DEBUG_LOGGING_ENABLED", "false") == "true",
		TelemetryEnabled:       false, // resolved below, from consent or an explicit override
		TelemetryEndpoint:      env.Get("SCT__TELEMETRY_ENDPOINT", firstNonEmpty(fv.telemetryEndpoint, defaultLocalEndpoint)),
		TelemetryTokenEnv:      env.Get("SCT__TELEMETRY_TOKEN", ""),
		WorkspaceProxyURL:      env.Get("SCT__WORKSPACE_PROXY_URL", firstNonEmpty(fv.workspaceProxyURL, defaultWorkspaceProxy)),
		LegacyToken:            fv.telemetryToken,
		OrgTokens:              fv.orgTokens,
		DefaultOrg:             env.Get("SCT__TELEMETRY_DEFAULT_ORG", fv.defaultOrg),
		ForceTier:              env.Get("SCT__FORCE_TIER", ""),
		StatsDBPath:            env.Get("SCT__STATS_DB_PATH", filepath.Join(base, "stats.db")),
		SpoolDir:               env.Get("SCT__SPOOL_DIR", filepath.Join(base, "spool")),
		ConfigFilePath:         configPath,
	}

	disclosure, _ := strconv.Atoi(fv.consentDisclosure)
	cfg.Consent = ConsentRecord{Decision: fv.consent, At: fv.consentAt, Disclosure: disclosure}

	// Precedence, and the ORDER is the policy. An explicit telemetry_enabled —
	// environment or file — wins in BOTH directions: somebody who typed it meant
	// it, and a central rollout needs to set the answer without a prompt nobody
	// will see. Only when nothing explicit exists does consent decide, and an
	// absent or stale decision means OFF.
	explicit := env.Get("SCT__TELEMETRY_ENABLED", fv.telemetryEnabled)
	switch {
	case explicit != "":
		cfg.TelemetryExplicit = true
		cfg.ServiceTelemetryEnabled = explicit == "true"
		cfg.ImprovementTelemetryEnabled = explicit == "true"
	default:
		// Service data is the customer's own report and needs no prompt; without
		// an API key TokenForOrg refuses delivery anyway. Improvement data is
		// opt-in because it is aggregated across customers.
		cfg.ServiceTelemetryEnabled = true
		cfg.ImprovementTelemetryEnabled = cfg.Consent.Grants()
	}
	// TelemetryEnabled stays as "is anything collected at all", which is what the
	// emitter construction and `sctx doctor` mean by it.
	cfg.TelemetryEnabled = cfg.ServiceTelemetryEnabled || cfg.ImprovementTelemetryEnabled

	raw := env.Get("SCT__MAX_OUTPUT_BYTES", "8388608")
	cfg.MaxOutputBytes, err = strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("parsing SCT__MAX_OUTPUT_BYTES: %w", err)
	}

	return cfg, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// fileValues holds the subset of ~/.config/sctx/config.toml keys sctx
// understands. Unknown keys are ignored (forward-compatible), not an error.
type fileValues struct {
	telemetryEndpoint string
	workspaceProxyURL string
	telemetryToken    string
	telemetryEnabled  string
	defaultOrg        string
	consent           string
	consentAt         string
	consentDisclosure string
	// orgTokens holds token = "..." keys found under [org.<slug>] sections,
	// keyed by slug. Lazily initialized; nil when no sections are present.
	orgTokens map[string]string
}

// loadConfigFile reads the optional config file. This must never fail Load:
// a missing file is silent; a malformed or unreadable file prints exactly
// one warning to stderr and the caller falls back to env-only configuration
// (an empty fileValues).
func loadConfigFile(path string) fileValues {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "sctx: warning: reading config file %s: %v (falling back to env-only configuration)\n", path, err)
		}
		return fileValues{}
	}

	var fv fileValues
	// section is "" at top level, "org:<slug>" inside a recognized [org.<slug>]
	// section, or "?" inside an unknown section (keys ignored, forward-compat).
	section := ""
	for i, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if slug, ok := strings.CutPrefix(name, "org."); ok && slug != "" {
				section = "org:" + slug
			} else {
				section = "?"
			}
			continue
		}
		key, value, ok := parseConfigLine(line)
		if !ok {
			fmt.Fprintf(os.Stderr, "sctx: warning: config file %s: malformed line %d, ignoring file (falling back to env-only configuration)\n", path, i+1)
			return fileValues{}
		}
		if slug, ok := strings.CutPrefix(section, "org:"); ok {
			if key == "token" {
				if fv.orgTokens == nil {
					fv.orgTokens = make(map[string]string)
				}
				fv.orgTokens[slug] = value
			}
			continue
		}
		if section == "?" {
			continue
		}
		switch key {
		case "telemetry_endpoint":
			fv.telemetryEndpoint = value
		case "workspace_proxy_url":
			fv.workspaceProxyURL = value
		case "telemetry_token":
			fv.telemetryToken = value
		case "telemetry_enabled":
			fv.telemetryEnabled = value
		case "default_org":
			fv.defaultOrg = value
		case "telemetry_consent":
			fv.consent = value
		case "telemetry_consent_at":
			fv.consentAt = value
		case "telemetry_disclosure":
			fv.consentDisclosure = value
		}
	}
	return fv
}

// parseConfigLine parses one `key = "value"` line of the strict, zero-dep
// config file format: bare keys, double-quoted string values, no nesting,
// no multi-line values. Anything else is malformed.
func parseConfigLine(line string) (key, value string, ok bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	rawValue := strings.TrimSpace(line[idx+1:])
	if key == "" || len(rawValue) < 2 || rawValue[0] != '"' || rawValue[len(rawValue)-1] != '"' {
		return "", "", false
	}
	value = rawValue[1 : len(rawValue)-1]
	if strings.ContainsRune(value, '"') {
		return "", "", false
	}
	return key, value, true
}

// PermitsPurpose reports whether events collected for a purpose may be delivered.
// Satisfies spool.TokenResolver alongside TokenForOrg.
func (c Config) PermitsPurpose(purpose string) bool {
	switch purpose {
	case telemetry.PurposeService:
		return c.ServiceTelemetryEnabled
	default:
		// Improvement, and anything unclassified — the conservative side.
		return c.ImprovementTelemetryEnabled
	}
}
