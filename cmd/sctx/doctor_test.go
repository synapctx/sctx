package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = orig
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	return string(out)
}

// `sctx doctor` must say plainly whether redaction is active, and how to
// change it, since it is opt-in this release and otherwise invisible.
func TestDoctorReportsRedactionState(t *testing.T) {
	off := testConfig(t)
	out := captureStdout(t, func() { runDoctor(off) })
	if !strings.Contains(out, "redaction:      off (opt in with SCT__REDACT=true)") {
		t.Errorf("doctor output missing off-state redaction line:\n%s", out)
	}

	on := testConfig(t)
	on.Redact = true
	out = captureStdout(t, func() { runDoctor(on) })
	if !strings.Contains(out, "redaction:      on") {
		t.Errorf("doctor output missing on-state redaction line:\n%s", out)
	}
}
