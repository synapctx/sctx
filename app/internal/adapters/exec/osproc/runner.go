// Package osproc runs wrapped commands via os/exec with exact exit-code
// fidelity, signal forwarding to the child, and bounded-memory output capture.
//
// Signal delivery is the one genuinely platform-specific part and lives in
// procgroup_unix.go / procgroup_windows.go.
package osproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/platform/iospill"
)

// ExitInternalError is the sctx exit code when the command could not be
// started at all (mirrors the shell's 127 "command not found").
const ExitInternalError = 127

type Runner struct {
	spillThreshold int64
}

func NewRunner(spillThreshold int64) *Runner {
	return &Runner{spillThreshold: spillThreshold}
}

func (r *Runner) Run(ctx context.Context, c exec.Command) (exec.Outcome, error) {
	if len(c.Argv) == 0 {
		return exec.Outcome{ExitCode: ExitInternalError}, errors.New("empty argv")
	}

	stdout := iospill.New(r.spillThreshold)
	stderr := iospill.New(r.spillThreshold)

	cmd := osexec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	if c.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if c.Tee != nil {
		cmd.Stdout = io.MultiWriter(stdout, c.Tee)
	} else {
		cmd.Stdout = stdout
	}
	cmd.Stderr = stderr
	// Own process group so signals reach the whole child tree, where the platform
	// has such a thing — see procgroup_unix.go and procgroup_windows.go.
	cmd.SysProcAttr = newProcessGroupAttr()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return exec.Outcome{ExitCode: ExitInternalError}, fmt.Errorf("starting %s: %w", c.Argv[0], err)
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				forwardSignal(cmd.Process, sig)
			case <-done:
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	signal.Stop(sigCh)

	out := exec.Outcome{
		Stdout:   stdout,
		Stderr:   stderr,
		Duration: time.Since(start),
	}

	if waitErr == nil {
		return out, nil
	}

	var exitErr *osexec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			out.Signaled = true
			out.ExitCode = 128 + int(status.Signal())
		} else {
			out.ExitCode = exitErr.ExitCode()
		}
		return out, nil
	}

	out.ExitCode = ExitInternalError
	return out, fmt.Errorf("running %s: %w", c.Argv[0], waitErr)
}
