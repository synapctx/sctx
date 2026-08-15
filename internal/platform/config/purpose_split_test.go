package config

import (
	"strconv"
	"testing"

	"github.com/synapctx/sctx/internal/domain/telemetry"
)

// THE regression this split exists to fix. The first cut gated everything behind
// one prompt, so a paying customer who declined opened their console and saw zero
// savings — we withheld the customer's own report about the tool they bought, to
// protect them from it.
func TestDecliningDoesNotDarkenTheCustomersOwnSavingsReport(t *testing.T) {
	withConfig(t, `telemetry_consent = "declined"
telemetry_disclosure = "`+strconv.Itoa(CurrentDisclosure)+`"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PermitsPurpose(telemetry.PurposeService) {
		t.Error("a refusal switched off the customer's OWN savings dashboard — that is their data, about the product they bought")
	}
	if cfg.PermitsPurpose(telemetry.PurposeImprovement) {
		t.Error("a refusal did not stop the data we aggregate across customers")
	}
}

// The other half: consent buys us the improvement signal, and nothing more.
func TestGrantingEnablesImprovementCollection(t *testing.T) {
	withConfig(t, `telemetry_consent = "granted"
telemetry_disclosure = "`+strconv.Itoa(CurrentDisclosure)+`"
`)
	cfg, _ := Load()
	if !cfg.PermitsPurpose(telemetry.PurposeImprovement) || !cfg.PermitsPurpose(telemetry.PurposeService) {
		t.Error("consent did not enable both purposes")
	}
}

// Someone who has never answered still gets their own dashboards. The question
// is only ever about the part we aggregate.
func TestAnUnansweredCustomerStillGetsTheirOwnDashboards(t *testing.T) {
	withConfig(t, "")
	cfg, _ := Load()
	if !cfg.PermitsPurpose(telemetry.PurposeService) {
		t.Error("an unanswered customer lost their savings report")
	}
	if cfg.PermitsPurpose(telemetry.PurposeImprovement) {
		t.Error("silence was read as consent for the aggregated half")
	}
}

// An explicit setting is a total answer: it exists so a rollout can switch
// everything off, including the part that would otherwise ride on the API key.
func TestAnExplicitOffStopsEverything(t *testing.T) {
	withConfig(t, "")
	t.Setenv("SCT__TELEMETRY_ENABLED", "false")
	cfg, _ := Load()
	if cfg.PermitsPurpose(telemetry.PurposeService) || cfg.PermitsPurpose(telemetry.PurposeImprovement) {
		t.Error("an explicit off left a purpose enabled")
	}
	if cfg.TelemetryEnabled {
		t.Error("TelemetryEnabled must be false when neither purpose is permitted")
	}
}

// A kind nobody has classified must need consent, not ride along as service data.
// The opposite default is how a payload quietly outgrows what customers agreed to.
func TestAnUnclassifiedKindNeedsConsent(t *testing.T) {
	withConfig(t, `telemetry_consent = "declined"
telemetry_disclosure = "2"
`)
	cfg, _ := Load()
	if cfg.PermitsPurpose(telemetry.PurposeOf("some_future_kind")) {
		t.Error("an unclassified event kind was treated as service data")
	}
}
