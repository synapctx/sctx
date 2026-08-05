package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/platform/config"
)

// THE guarantee. The hook path has no terminal and no human; a blocking prompt
// there would stall every Bash command the agent runs, forever, waiting for an
// answer nobody can give.
func TestConsentIsNeverAskedWithoutATerminal(t *testing.T) {
	if shouldAskConsent(config.Config{}, false) {
		t.Error("would prompt with no terminal — this blocks the hook path")
	}
}

func TestConsentIsAskedOnceWhenNobodyHasAnswered(t *testing.T) {
	if !shouldAskConsent(config.Config{}, true) {
		t.Error("did not ask an unanswered customer")
	}
	answered := config.Config{Consent: config.NewConsent(config.ConsentDeclined, time.Now())}
	if shouldAskConsent(answered, true) {
		t.Error("re-asked someone who already said no — that is nagging, and a refusal IS an answer")
	}
}

// Someone who set telemetry_enabled has answered by configuration. Prompting
// implies the answer matters when it would be overridden either way.
func TestSomeoneWhoAnsweredByConfigurationIsNotAsked(t *testing.T) {
	if shouldAskConsent(config.Config{TelemetryExplicit: true}, true) {
		t.Error("prompted over an explicit setting")
	}
}

// A prompt whose default is yes is a dark pattern wearing a consent prompt's
// clothes. Someone pressing return to get through an install has agreed to
// nothing.
func TestPressingReturnDeclines(t *testing.T) {
	for _, answer := range []string{"\n", "\r\n", "   \n"} {
		var out bytes.Buffer
		cfg := config.Config{ConfigFilePath: t.TempDir() + "/config.toml"}
		askConsent(cfg, strings.NewReader(answer), &out)
		if !strings.Contains(out.String(), "Nothing will be sent") {
			t.Errorf("answer %q was not treated as a refusal:\n%s", answer, out.String())
		}
	}
}

func TestOnlyAnExplicitYesGrants(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", "YES\n"} {
		var out bytes.Buffer
		cfg := config.Config{ConfigFilePath: t.TempDir() + "/config.toml"}
		askConsent(cfg, strings.NewReader(answer), &out)
		if !strings.Contains(out.String(), "Thank you") {
			t.Errorf("answer %q was not treated as consent:\n%s", answer, out.String())
		}
	}
	for _, answer := range []string{"n\n", "no\n", "maybe\n", "sure\n"} {
		var out bytes.Buffer
		cfg := config.Config{ConfigFilePath: t.TempDir() + "/config.toml"}
		askConsent(cfg, strings.NewReader(answer), &out)
		if strings.Contains(out.String(), "Thank you") {
			t.Errorf("answer %q was read as consent", answer)
		}
	}
}

// The status line must distinguish the three ways telemetry can be off: nobody
// asked, they refused, or a central policy set it. Each has a different fix, and
// a bare "off" sends the reader to the wrong one.
func TestTheStatusLineSaysWhyNotJustWhat(t *testing.T) {
	unasked := telemetryStatusLine(config.Config{})
	declined := telemetryStatusLine(config.Config{Consent: config.NewConsent(config.ConsentDeclined, time.Now())})
	policy := telemetryStatusLine(config.Config{TelemetryExplicit: true})
	stale := telemetryStatusLine(config.Config{Consent: config.ConsentRecord{Decision: config.ConsentGranted, Disclosure: 0}})

	for name, got := range map[string]string{"unasked": unasked, "declined": declined, "policy": policy, "stale": stale} {
		if !strings.Contains(got, "OFF") {
			t.Errorf("%s status does not say OFF: %q", name, got)
		}
	}
	if unasked == declined || declined == policy || unasked == policy || stale == unasked {
		t.Error("two different reasons for OFF produced the same message; the reader cannot tell which fix applies")
	}
}
