package osproc

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	domexec "github.com/synapctx/sctx/internal/domain/exec"
)

func run(t *testing.T, argv ...string) domexec.Outcome {
	t.Helper()
	r := NewRunner(1 << 20)
	out, err := r.Run(context.Background(), domexec.Command{Argv: argv, Stdin: strings.NewReader("")})
	if err != nil {
		t.Fatalf("Run(%v): %v", argv, err)
	}
	t.Cleanup(func() {
		out.Stdout.Close()
		out.Stderr.Close()
	})
	return out
}

func readSpill(t *testing.T, s domexec.Spill) string {
	t.Helper()
	r, err := s.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(b)
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want int
	}{
		{"success", []string{"sh", "-c", "exit 0"}, 0},
		{"failure", []string{"sh", "-c", "exit 1"}, 1},
		{"arbitrary code", []string{"sh", "-c", "exit 42"}, 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, tt.argv...).ExitCode; got != tt.want {
				t.Fatalf("exit code = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCommandNotFound(t *testing.T) {
	r := NewRunner(1 << 20)
	out, err := r.Run(context.Background(), domexec.Command{Argv: []string{"definitely-not-a-real-binary-xyz"}})
	if err == nil {
		t.Fatal("expected an error for a missing binary")
	}
	if out.ExitCode != ExitInternalError {
		t.Fatalf("exit code = %d, want %d", out.ExitCode, ExitInternalError)
	}
}

func TestStreamsSeparated(t *testing.T) {
	out := run(t, "sh", "-c", "echo to-stdout; echo to-stderr 1>&2")
	if got := readSpill(t, out.Stdout); got != "to-stdout\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := readSpill(t, out.Stderr); got != "to-stderr\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestLargeOutputSpills(t *testing.T) {
	r := NewRunner(1024) // 1 KiB threshold forces the spill path
	out, err := r.Run(context.Background(), domexec.Command{
		Argv: []string{"sh", "-c", `i=0; while [ $i -lt 1000 ]; do echo "line $i of filler output"; i=$((i+1)); done`},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer out.Stdout.Close()
	defer out.Stderr.Close()

	if !out.Stdout.Spilled() {
		t.Fatal("expected stdout to spill to disk")
	}
	content := readSpill(t, out.Stdout)
	if !strings.Contains(content, "line 999 of filler output") {
		t.Fatal("spilled content is incomplete")
	}
	if int64(len(content)) != out.Stdout.Len() {
		t.Fatalf("Len() = %d, content length = %d", out.Stdout.Len(), len(content))
	}
	// Bytes must be re-readable.
	if again := readSpill(t, out.Stdout); again != content {
		t.Fatal("second Bytes() read differs from the first")
	}
}

// A background grandchild that inherits stdout must not hold sctx after the
// command itself has exited: Wait is bounded by waitDelay, the output written
// before that is kept and the exit code is the command's own.
func TestBackgroundGrandchildDoesNotHang(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		wantCode int
	}{
		{"success", "(sleep 6 &); echo main-exited", 0},
		{"failure", "(sleep 6 &); echo main-exited; exit 7", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			out := run(t, "sh", "-c", tt.script)
			if d := time.Since(start); d >= 4*time.Second {
				t.Fatalf("Run took %v, want < 4s", d)
			}
			if out.ExitCode != tt.wantCode {
				t.Errorf("exit code = %d, want %d", out.ExitCode, tt.wantCode)
			}
			if got := readSpill(t, out.Stdout); got != "main-exited\n" {
				t.Errorf("stdout = %q, want main-exited", got)
			}
		})
	}
}

// When a background process holds the pipes past waitDelay and the command
// itself succeeded, the cut is reported on the Outcome (the service prints the
// notice after the command's own output); a clean run never sets it.
func TestOutputCutIsReported(t *testing.T) {
	cut := run(t, "sh", "-c", "(sleep 6 &); echo main-exited")
	if !cut.OutputCut || cut.ExitCode != 0 {
		t.Errorf("OutputCut=%v exit=%d, want true/0", cut.OutputCut, cut.ExitCode)
	}
	clean := run(t, "sh", "-c", "echo hi")
	if clean.OutputCut {
		t.Errorf("OutputCut set for a clean run")
	}
}
