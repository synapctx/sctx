package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureDoctorStdout redirects os.Stdout for the duration of fn and returns
// everything written to it.
func captureDoctorStdout(t *testing.T, fn func()) string {
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
	out := captureDoctorStdout(t, func() { runDoctor(off) })
	if !strings.Contains(out, "redaction:      off (opt in with SCT__REDACT=true)") {
		t.Errorf("doctor output missing off-state redaction line:\n%s", out)
	}

	on := testConfig(t)
	on.Redact = true
	out = captureDoctorStdout(t, func() { runDoctor(on) })
	if !strings.Contains(out, "redaction:      on") {
		t.Errorf("doctor output missing on-state redaction line:\n%s", out)
	}
}

// fakeSctxBinary writes an executable at dir/sctxExeName() that, when run as
// "<path> version", prints version to stdout. Returns the executable's path.
func fakeSctxBinary(t *testing.T, dir, version string) string {
	t.Helper()
	path := filepath.Join(dir, sctxExeName())
	script := "#!/bin/sh\necho '" + version + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake sctx binary: %v", err)
	}
	return path
}

// TestPrintBinaryReportMarksEntriesAfterTheFirstAsShadowed proves the FIRST
// PATH entry (the one that actually runs) is reported plainly, and every
// entry AFTER it — dead weight PATH resolution never reaches — is marked
// [shadowed by the first]. This used to be inverted: the entry that runs was
// the one marked "SHADOWS", which told a developer reading their own PATH
// order exactly backwards.
func TestPrintBinaryReportMarksEntriesAfterTheFirstAsShadowed(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	fakeSctxBinary(t, firstDir, "sctx 0.7.0")
	fakeSctxBinary(t, secondDir, "sctx dev")

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })
	os.Setenv("PATH", firstDir+string(os.PathListSeparator)+secondDir)

	var buf bytes.Buffer
	printBinaryReport(&buf)
	out := buf.String()

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var firstLine, secondLine string
	for _, l := range lines {
		if strings.Contains(l, firstDir) {
			firstLine = l
		}
		if strings.Contains(l, secondDir) {
			secondLine = l
		}
	}
	if firstLine == "" || secondLine == "" {
		t.Fatalf("expected both binaries reported, got:\n%s", out)
	}
	if strings.Contains(firstLine, "shadowed") || strings.Contains(firstLine, "SHADOWS") {
		t.Errorf("the FIRST (running) entry must not be marked shadowed: %q", firstLine)
	}
	if !strings.Contains(secondLine, "[shadowed by the first]") {
		t.Errorf("the entry after the first must be marked [shadowed by the first]: %q", secondLine)
	}
}
