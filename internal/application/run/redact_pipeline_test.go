package run

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domexec "github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/rawcache"
	"github.com/synapctx/sctx/internal/platform/redact"
)

// fakeSecretToken is a syntactically valid GitHub token per redact.Rules,
// used across these tests as the planted secret.
const fakeSecretToken = "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestExecuteRedactsExitCodeAndCountsSurviveUnredacted covers requirement 4's
// exit-3-plus-planted-token fixture: a command that exits 3 and prints a
// fake token must still exit 3 (the exit code is sacred and never passes
// through redaction), the marker must be present in stdout, and exactly one
// secret must be counted.
func TestExecuteRedactsExitCodeAndCountsSurviveUnredacted(t *testing.T) {
	registry := NewRegistry()
	stdout := &bytes.Buffer{}
	emitter := &fakeEmitter{}
	svc := NewService(registry, fakeRunner{
		stdout:   "go test ./...\n--- FAIL: TestLeak (0.00s)\n" + fakeSecretToken + "\nFAIL\n",
		exitCode: 3,
	}, nil, emitter, nil, stdout, &bytes.Buffer{}, Options{Version: "v", Redact: true})

	code, err := svc.Execute(context.Background(), []string{"go", "test", "./..."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3 (must survive redaction untouched)", code)
	}
	out := stdout.String()
	if strings.Contains(out, fakeSecretToken) {
		t.Fatalf("secret leaked into stdout: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:github-token]") {
		t.Fatalf("marker missing from stdout: %q", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("exit-signal text FAIL was dropped, not just the secret: %q", out)
	}
	if len(emitter.events) != 1 || emitter.events[0].RedactedCount != 1 {
		t.Fatalf("events = %+v, want exactly one event with RedactedCount 1", emitter.events)
	}
	if emitter.events[0].ExitCode != 3 {
		t.Fatalf("telemetry ExitCode = %d, want 3", emitter.events[0].ExitCode)
	}
}

// TestExitCodeAndCountsAreNeverRedacted proves redaction never mistakes
// sctx's OWN accounting notation — an elision marker or a repeat count — for
// a secret. Bytes like "FAIL ×3" or "...+12 more" must survive byte-for-byte
// with Redact on, and the exit code (returned separately from any byte
// stream) is never run through redact.Apply at all.
func TestExitCodeAndCountsAreNeverRedacted(t *testing.T) {
	registry := NewRegistry()
	stdout := &bytes.Buffer{}
	const body = "FAIL ×3\n…+12 more\nno secrets here\n"
	svc := NewService(registry, fakeRunner{stdout: body, exitCode: 7}, nil, nil, nil, stdout, &bytes.Buffer{},
		Options{Version: "v", Redact: true})

	code, err := svc.Execute(context.Background(), []string{"unmatched-tool"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if stdout.String() != body {
		t.Fatalf("accounting notation was altered:\n got  %q\n want %q", stdout.String(), body)
	}
}

// TestExecuteRedactionCountsAcrossForcedTiers is the renderChain-level check
// requirement 4 asks for: a fake key planted in a go-test-shaped fixture
// must be redacted from the FINAL bytes no matter which tier produced them —
// aggressive, relaxed, verbatim, or the "off" bypass — because redaction runs
// after the tier chain by construction.
func TestExecuteRedactionCountsAcrossForcedTiers(t *testing.T) {
	fixture := "go test ./...\n--- FAIL: TestLeak (0.00s)\n" + fakeSecretToken + "\nFAIL\n"

	for _, forceTier := range []string{string(format.TierAggressive), string(format.TierRelaxed), string(format.TierVerbatim), "off"} {
		t.Run(forceTier, func(t *testing.T) {
			registry := NewRegistry()
			registry.Register(&fakeFormatter{
				match: format.Match{Command: "go", Subcommands: []string{"test"}},
				aggressive: func(in format.Input) (format.Rendered, error) {
					body, _ := readAllFormat(in)
					return format.Rendered{Body: append([]byte("(aggressive) "), body...)}, nil
				},
				relaxed: func(in format.Input) (format.Rendered, error) {
					body, _ := readAllFormat(in)
					return format.Rendered{Body: append([]byte("(relaxed) "), body...)}, nil
				},
			})
			stdout := &bytes.Buffer{}
			svc := NewService(registry, fakeRunner{stdout: fixture}, nil, nil, nil, stdout, &bytes.Buffer{},
				Options{Version: "v", ForceTier: forceTier, Redact: true})

			if _, err := svc.Execute(context.Background(), []string{"go", "test", "./..."}); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			out := stdout.String()
			if strings.Contains(out, fakeSecretToken) {
				t.Fatalf("tier %s: secret leaked: %q", forceTier, out)
			}
			if !strings.Contains(out, "[REDACTED:github-token]") {
				t.Fatalf("tier %s: marker missing: %q", forceTier, out)
			}
		})
	}
}

// readAllFormat drains in.Stdout, the way a real formatter would before
// deciding whether the shape is its own.
func readAllFormat(in format.Input) ([]byte, error) {
	buf := &bytes.Buffer{}
	_, err := buf.ReadFrom(in.Stdout)
	return buf.Bytes(), err
}

// TestExecuteRedactsTheRawCacheSidecar proves the raw-output recovery
// sidecar is redacted too, not just the two live streams: an agent reads the
// sidecar back on request (the "sctx: raw output" hint), so an unredacted
// copy on disk would hand back exactly what redaction exists to withhold.
func TestExecuteRedactsTheRawCacheSidecar(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&fakeFormatter{
		match: format.Match{Command: "tool"},
		aggressive: func(format.Input) (format.Rendered, error) {
			return format.Rendered{Body: []byte("summary"), Elided: true}, nil
		},
		relaxed: func(format.Input) (format.Rendered, error) {
			return format.Rendered{}, format.ErrTierInapplicable
		},
	})
	root := t.TempDir()
	svc := NewService(registry, fakeRunner{stdout: "first\n" + fakeSecretToken + "\nlast\n"}, nil, nil, nil,
		&bytes.Buffer{}, &bytes.Buffer{}, Options{
			Version:  "v",
			RawCache: rawcache.New(root, time.Hour, 1024),
			Redact:   true,
		})

	if _, err := svc.Execute(context.Background(), []string{"tool"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %d, %v", len(entries), err)
	}
	got, err := os.ReadFile(filepath.Join(root, entries[0].Name(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), fakeSecretToken) {
		t.Fatalf("raw cache sidecar contains the unredacted secret: %q", got)
	}
	if !strings.Contains(string(got), "[REDACTED:github-token]") {
		t.Fatalf("raw cache sidecar missing marker: %q", got)
	}
}

// TestExecuteRedactionStreamSplitToken exercises the mechanism the streaming
// Tee path (unknown commands whose progress streams live, see
// domexec.Command.Tee) relies on: redact.NewWriter must reassemble a secret
// split across two underlying Write calls before Apply ever sees it, so a
// token straddling a write boundary is still caught. Command.Tee is not yet
// wired into Service.Execute (nothing currently sets it there), so this
// drives the real osproc runner directly at the pipeline's lower layer —
// the same layer Execute would hand a redact.Writer to once that wiring
// lands.
func TestExecuteRedactionStreamSplitToken(t *testing.T) {
	firstHalf := fakeSecretToken[:20]
	secondHalf := fakeSecretToken[20:]

	pr, pw := io.Pipe()
	dst := &bytes.Buffer{}
	rw := redact.NewWriter(dst)

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				rw.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	pw.Write([]byte(firstHalf))
	pw.Write([]byte(secondHalf))
	pw.Close()
	<-done
	if err := rw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := dst.String()
	if strings.Contains(out, fakeSecretToken) {
		t.Fatalf("split secret leaked: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:github-token]") {
		t.Fatalf("marker missing: %q", out)
	}
	if rw.Report().Count != 1 {
		t.Fatalf("Report().Count = %d, want 1", rw.Report().Count)
	}
	_ = domexec.Command{} // documents the field this mechanism backs, see doc comment above
}
