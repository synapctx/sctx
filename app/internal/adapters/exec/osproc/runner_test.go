package osproc

import (
	"context"
	"io"
	"strings"
	"testing"

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
		{"success", []string{"/bin/sh", "-c", "exit 0"}, 0},
		{"failure", []string{"/bin/sh", "-c", "exit 1"}, 1},
		{"arbitrary code", []string{"/bin/sh", "-c", "exit 42"}, 42},
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
	out := run(t, "/bin/sh", "-c", "echo to-stdout; echo to-stderr 1>&2")
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
		Argv: []string{"/bin/sh", "-c", `i=0; while [ $i -lt 1000 ]; do echo "line $i of filler output"; i=$((i+1)); done`},
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
