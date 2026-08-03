// Package exec defines the port for running a wrapped command with exact
// exit-code fidelity and bounded-memory output capture.
package exec

import (
	"context"
	"io"
	"time"
)

// Command describes the child process to run.
type Command struct {
	Argv  []string
	Dir   string
	Env   []string
	Stdin io.Reader
	// Tee, when non-nil, receives stdout live while it is also captured.
	// Used for unknown commands so the agent sees progress on long runs.
	Tee io.Writer
}

// Outcome is the result of running a command. Stdout and Stderr are fully
// captured (spilling to disk past a threshold) and readable after Run
// returns.
type Outcome struct {
	// ExitCode is the child's exact exit code; 128+signum when the child
	// was terminated by a signal.
	ExitCode int
	Signaled bool
	Stdout   Spill
	Stderr   Spill
	Duration time.Duration
}

// Runner executes a command to completion, forwarding SIGINT/SIGTERM to the
// child's process group. A non-nil error means sctx itself failed to run the
// command (e.g. binary not found), not that the command exited non-zero.
type Runner interface {
	Run(ctx context.Context, c Command) (Outcome, error)
}

// Spill is captured process output: in memory up to a threshold, on disk
// beyond it. Bytes returns a reader positioned at the start; it may be
// called more than once.
type Spill interface {
	Bytes() (io.Reader, error)
	Len() int64
	Spilled() bool
	Close() error
}
