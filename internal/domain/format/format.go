// Package format defines the formatter port: how sctx re-renders a wrapped
// command's output in a token-minimal form, and the tier model used to
// degrade safely when a structured parse is not possible.
package format

import (
	"context"
	"errors"
	"io"
	"time"
)

// Tier identifies how a command's output was rendered. Tiers are attempted
// aggressive → relaxed → verbatim; a failure at one tier degrades to the
// next and never suppresses output.
type Tier string

const (
	// TierAggressive is a structured parse of a known output format with
	// maximum compression.
	TierAggressive Tier = "aggressive"
	// TierRelaxed is heuristic line-level filtering: deduplication, noise
	// stripping, error retention.
	TierRelaxed Tier = "relaxed"
	// TierVerbatim is raw passthrough, owned by the tier chain rather than
	// individual formatters.
	TierVerbatim Tier = "verbatim"
)

// ErrTierInapplicable signals that a tier does not apply to this input and
// the chain should silently try the next tier. Any other error from a tier
// is recorded as an anomaly before degrading.
var ErrTierInapplicable = errors.New("format: tier inapplicable")

// Input carries everything a formatter may need to render a command's output.
type Input struct {
	Argv     []string
	Command  string // normalized key, e.g. "go test" or "git"
	Stdout   io.Reader
	Stderr   io.Reader
	ExitCode int
	Duration time.Duration
}

// Rendered is a tier's output.
type Rendered struct {
	// Body is emitted on stdout in place of the raw output.
	Body []byte
	// Note is an optional short annotation recorded with the run.
	Note string
	// FoldStderr indicates the formatter absorbed stderr into Body, so the
	// pipeline must not re-emit raw stderr.
	FoldStderr bool
}

// Match declares which commands a formatter claims. Command is matched on
// the basename of argv[0]; Subcommands, when set, must be an ordered prefix
// of the non-flag arguments. Longer Subcommands win, then higher Priority.
type Match struct {
	Command     string
	Subcommands []string
	Priority    int
}

// Formatter renders one program's output. Subcommand dispatch (e.g. git
// status vs git diff) is handled inside the formatter, keeping the registry
// one entry per program.
type Formatter interface {
	Descriptor() Match
	Aggressive(ctx context.Context, in Input) (Rendered, error)
	Relaxed(ctx context.Context, in Input) (Rendered, error)
}
