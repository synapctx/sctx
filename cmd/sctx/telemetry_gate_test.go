package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
)

// --disable promises the queue is discarded. A spool left on disk after a
// refusal is the refused data still sitting there, one `sctx flush` from being
// sent — and a promise that quietly does nothing is worse than no promise.
func TestDisableActuallyDiscardsTheQueue(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "pending.jsonl")
	if err := os.WriteFile(spool, []byte("{\"a\":1}\n{\"b\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := discardSpool(config.Config{SpoolDir: dir})
	if err != nil {
		t.Fatalf("discardSpool: %v", err)
	}
	if n != 2 {
		t.Errorf("reported %d discarded, want 2", n)
	}
	if _, err := os.Stat(spool); !os.IsNotExist(err) {
		t.Error("the queue survived a refusal")
	}
}

func TestDiscardingAnAbsentQueueIsNotAnError(t *testing.T) {
	n, err := discardSpool(config.Config{SpoolDir: t.TempDir()})
	if err != nil || n != 0 {
		t.Errorf("n=%d err=%v, want 0 and no error", n, err)
	}
}

// The status must report BOTH purposes independently. A single "OFF" was a lie
// after the split: a customer who declined still has their savings report
// flowing, and telling them otherwise is how they conclude it is broken.
func TestTheStatusReportsBothPurposesIndependently(t *testing.T) {
	declined := config.Config{
		ServiceTelemetryEnabled:     true,
		ImprovementTelemetryEnabled: false,
		Consent:                     config.ConsentRecord{Decision: config.ConsentDeclined, Disclosure: config.CurrentDisclosure},
	}
	line := telemetryStatusLine(declined)
	if !strings.Contains(line, "Your savings report: ON") {
		t.Errorf("told a declining customer their savings report is off:\n%s", line)
	}
	if !strings.Contains(line, "Commands we fail to cover: OFF") {
		t.Errorf("did not report the refusal:\n%s", line)
	}

	off := telemetryStatusLine(config.Config{TelemetryExplicit: true})
	if !strings.Contains(off, "Your savings report: OFF") || !strings.Contains(off, "console.synapctx.com will show nothing") {
		t.Errorf("an explicit off must warn that the dashboards go empty:\n%s", off)
	}
}
