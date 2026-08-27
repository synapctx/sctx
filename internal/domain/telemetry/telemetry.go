// Package telemetry defines the fire-and-forget usage-event port feeding the
// SynapCTX platform (org-wide savings on the management console).
package telemetry

import (
	"context"
	"time"
)

// KindExecSavings is the usage-event kind emitted for every wrapped command.
const KindExecSavings = "exec_savings"

// KindCoverageGap is the usage-event kind emitted when the Claude Code hook sees
// a command sctx has no rewrite rule for. Ranked by frequency it answers "which
// formatter is worth building next", measured against what developers actually
// run rather than what we imagine they run.
//
// Only the program (or "program subcommand") is recorded, never arguments —
// see internal/platform/progkey for why token shape cannot be trusted to tell an
// operation from a path.
const KindCoverageGap = "coverage_gap"

// KindCoverageDecline records a known command the hook intentionally left
// untouched for buffering or shell-safety reasons. Keeping it separate from a
// coverage gap prevents deliberate safety choices from inflating formatter
// demand.
const KindCoverageDecline = "coverage_decline"

// Formatter selection is independent from the render outcome. A dedicated
// formatter may decline to verbatim, while the generic formatter may produce a
// real saving; one boolean cannot represent both facts.
const (
	FormatterKindDedicated = "dedicated"
	FormatterKindGeneric   = "generic"
	FormatterKindNone      = "none"
)

// Privacy-safe reasons why an invocation produced no measured saving. They
// classify behavior only and never contain arguments, paths, or output.
const (
	DeclineUnsupportedCommand    = "unsupported_command"
	DeclineUnsupportedSubcommand = "unsupported_subcommand"
	DeclineArbitraryOutput       = "arbitrary_output"
	DeclineUnrecognizedOutput    = "unrecognized_output"
	DeclineSilentCommand         = "silent_command"
	DeclineExplicitBypass        = "explicit_bypass"
	DeclineNoNetSaving           = "no_net_saving"
	DeclineStreaming             = "streaming"
	DeclineUnsafeShellShape      = "unsafe_shell_shape"
	DeclineDownstreamTransform   = "downstream_transforming_pipeline"
)

// Event is one exec-savings usage event.
type Event struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Tool    string `json:"tool"`
	Version string `json:"version"`
	// RepositoryName is the "org/repo" of the git repository the command
	// ran in, detected from local git metadata; empty when not in a repo
	// or the origin remote is unparseable.
	RepositoryName string `json:"repositoryName,omitempty"`
	Command        string `json:"command"`
	// Program is the stable aggregation key: the program plus its first
	// subcommand (e.g. "go test", "terraform plan"). Command is retained for
	// compatibility and carries the same privacy-safe key, never argv.
	Program     string `json:"program,omitempty"`
	Tier        string `json:"tier"`
	RawTokens   int64  `json:"rawTokens"`
	OutTokens   int64  `json:"outTokens"`
	SavedTokens int64  `json:"savedTokens"`
	ExitCode    int    `json:"exitCode"`
	DurationMS  int64  `json:"durationMs"`
	// FormatterMatched is retained for older servers and means a dedicated
	// formatter was selected. It never meant that output was reduced.
	FormatterMatched bool      `json:"formatterMatched,omitzero"`
	FormatterKind    string    `json:"formatterKind,omitempty"`
	OutputReduced    bool      `json:"outputReduced,omitzero"`
	DeclineReason    string    `json:"declineReason,omitempty"`
	At               time.Time `json:"at"`
}

// Emitter accepts events without ever blocking on the network. Emit appends
// to a local spool and returns immediately; Flush opportunistically drains
// the spool under its own deadline. Telemetry failure must never slow or
// break a wrapped command.
type Emitter interface {
	Emit(ev Event)
	Flush(ctx context.Context) error
}
