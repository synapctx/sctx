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
	// Client is which coding agent ran this: "claude-code", "shell", etc — see
	// internal/platform/agentenv. Empty on rows recorded before this field
	// existed.
	Client string
	// SessionID is the agent's own opaque session id, best-effort.
	SessionID string
	// ArgvHash is the same salted argv fingerprint sent in telemetry — kept
	// locally too so `sctx gain` can report on it without a network round
	// trip. See telemetry.Event.ArgvHash.
	ArgvHash string
	// Bypass records how sctx's own formatting was skipped for this run: ""
	// (not bypassed), "force_tier" or "double_dash".
	Bypass string
	// RedactedCount is how many secrets a redaction pass hid before this run
	// was recorded. Reserved for a future redaction feature; always 0 today.
	RedactedCount int
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
	// ByClient aggregates runs matching opts by which coding agent ran them,
	// ordered by SavedTokens descending, for `sctx gain --by-client`. A row
	// with an empty Client groups runs recorded before this field existed, or
	// run from a plain shell.
	ByClient(ctx context.Context, opts AggregateOptions) ([]ClientTotals, error)
	// RepeatedRunsToday reports argv values run more than once since the
	// start of today (local time), newest-heaviest first, capped at limit.
	// Computed from the plain argv column, never ArgvHash — this is a local
	// report reading the local store, not anything that could leave the
	// machine.
	RepeatedRunsToday(ctx context.Context, limit int) ([]RepeatedRun, error)
	Close() error
}

// ClientTotals is Totals scoped to one coding-agent Client.
type ClientTotals struct {
	Client string `json:"client"`
	Totals
}

// RepeatedRun is one argv value run more than once today.
type RepeatedRun struct {
	Argv  string `json:"argv"`
	Count int64  `json:"count"`
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
