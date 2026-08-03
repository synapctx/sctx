// Package stats defines the local token-savings accounting model behind
// `sctx gain`.
package stats

import (
	"context"
	"time"
)

// Run records one wrapped command execution and its token accounting.
type Run struct {
	ID          string // ULID
	At          time.Time
	Command     string // normalized key, e.g. "go test"
	Argv        string // full argv, space-joined
	Formatter   string
	Tier        string // aggressive | relaxed | verbatim
	RawBytes    int64
	RawTokens   int64
	OutTokens   int64
	SavedTokens int64
	ExitCode    int
	DurationMS  int64
	Anomaly     string // empty when the render was clean
	Repository  string // "org/repo", best-effort (may be empty)
}

// AggregateOptions scopes an Aggregate or Failures query. The zero value
// means "no filter": all repositories, all time.
type AggregateOptions struct {
	Repository string    // exact "org/repo" match; empty = all repositories
	Since      time.Time // only runs at/after this instant; zero = all-time
}

// FailedRun is one entry in the degradation log: a run sctx could not
// compress (tier fell all the way to verbatim) or that hit a render
// anomaly.
type FailedRun struct {
	Command string // normalized key
	Argv    string // full argv, space-joined
	Tier    string
	Anomaly string
	At      time.Time
}

// Store persists runs and aggregates them for reporting.
type Store interface {
	Record(ctx context.Context, r Run) error
	// Aggregate summarizes runs matching opts.
	Aggregate(ctx context.Context, opts AggregateOptions) (Report, error)
	// Failures returns the most recent degraded runs matching opts (tier
	// "verbatim" or a non-empty Anomaly), newest first, capped at limit.
	Failures(ctx context.Context, opts AggregateOptions, limit int) ([]FailedRun, error)
	Close() error
}

// Report is the aggregate behind `sctx gain`.
type Report struct {
	Global    Totals
	ByCommand []CommandTotals
	Since     time.Time
	// TotalExecMS is the summed child wall-clock across all runs.
	TotalExecMS int64
}

// Totals accumulates token accounting over a set of runs.
type Totals struct {
	Runs        int64 `json:"runs"`
	RawTokens   int64 `json:"raw_tokens"`
	OutTokens   int64 `json:"out_tokens"`
	SavedTokens int64 `json:"saved_tokens"`
	AvgMS       int64 `json:"avg_ms"`
}

// CommandTotals is Totals scoped to one normalized command.
type CommandTotals struct {
	Command string `json:"command"`
	Totals
}
