package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func withConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows: os.UserHomeDir reads USERPROFILE, not HOME
	dir := filepath.Join(home, ".config", "sctx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Env must not leak in from the developer's own shell. t.Setenv registers the
	// restore, so unsetting afterwards is still cleaned up — and it must be
	// UNSET rather than empty: go-env treats a set-but-empty variable as a real
	// value, which would itself count as an explicit answer.
	t.Setenv("SCT__TELEMETRY_ENABLED", "")
	os.Unsetenv("SCT__TELEMETRY_ENABLED")
}

// THE behaviour change. telemetry_enabled used to default to true and nothing
// ever asked, so the data WE learn from left the machine because nobody had said
// no — which is not the same as somebody saying yes.
func TestWithNoDecisionTheAggregatedHalfIsOff(t *testing.T) {
	withConfig(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImprovementTelemetryEnabled {
		t.Error("a fresh install with no recorded decision is sending the data we aggregate")
	}
	if cfg.Consent.Answered() {
		t.Error("reported an answer where none was recorded")
	}
}

func TestAGrantedDecisionEnablesDelivery(t *testing.T) {
	withConfig(t, granted())
	cfg, _ := Load()
	if !cfg.ImprovementTelemetryEnabled {
		t.Error("a granted decision did not enable the improvement half")
	}
}

// Fixtures are built from CurrentDisclosure rather than a literal. A bump would
// otherwise make every "granted" fixture stale, quietly turning these into tests
// of the refusal path while still passing.
func granted() string {
	return "telemetry_consent = \"granted\"\ntelemetry_disclosure = \"" +
		strconv.Itoa(CurrentDisclosure) + "\"\n"
}

func declined() string {
	return "telemetry_consent = \"declined\"\ntelemetry_disclosure = \"" +
		strconv.Itoa(CurrentDisclosure) + "\"\n"
}

func TestADeclinedDecisionIsRespected(t *testing.T) {
	withConfig(t, declined())
	cfg, _ := Load()
	if cfg.ImprovementTelemetryEnabled {
		t.Error("the aggregated half is on despite a recorded refusal")
	}
	if !cfg.Consent.Answered() {
		t.Error("a refusal IS an answer; re-prompting someone who said no is nagging")
	}
}

// Consent to "program names and token counts" is not consent to whatever we
// decide to send later. A decision recorded against an older disclosure must not
// authorise a bigger payload.
func TestConsentToAnOlderDisclosureDoesNotAuthoriseTheCurrentPayload(t *testing.T) {
	withConfig(t, `telemetry_consent = "granted"
telemetry_disclosure = "0"
`)
	cfg, _ := Load()
	if cfg.ImprovementTelemetryEnabled {
		t.Error("an outdated consent authorised the current payload")
	}
	if !cfg.Consent.Stale() {
		t.Error("this is the 'we changed what we collect' case and must be distinguishable from never having asked")
	}
	if cfg.Consent.Answered() {
		t.Error("a stale decision must not count as answered, or it is never re-asked")
	}
}

// An explicit setting wins in BOTH directions: somebody who typed it meant it,
// and a central rollout needs to answer without a prompt nobody will see.
func TestAnExplicitSettingOverridesConsentBothWays(t *testing.T) {
	withConfig(t, declined()+"telemetry_enabled = \"true\"\n")
	cfg, _ := Load()
	if !cfg.ImprovementTelemetryEnabled || !cfg.TelemetryExplicit {
		t.Error("an explicit telemetry_enabled=true did not override a declined record")
	}

	withConfig(t, granted())
	t.Setenv("SCT__TELEMETRY_ENABLED", "false")
	cfg, _ = Load()
	if cfg.TelemetryEnabled || cfg.ServiceTelemetryEnabled {
		t.Error("SCT__TELEMETRY_ENABLED=false did not override a granted record")
	}
	if !cfg.TelemetryExplicit {
		t.Error("an env override must mark the matter as already decided, or setup prompts over it")
	}
}

func TestNewConsentStampsTheCurrentDisclosure(t *testing.T) {
	r := NewConsent(ConsentGranted, time.Now())
	if !r.Grants() || r.Disclosure != CurrentDisclosure {
		t.Errorf("record = %+v, want a granting record at disclosure %d", r, CurrentDisclosure)
	}
	if r.At == "" {
		t.Error("no timestamp recorded")
	}
}
