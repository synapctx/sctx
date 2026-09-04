// Package gofmt implements a format.Formatter for the `gofmt` CLI. `-l`
// lists non-conforming file paths and `-w` rewrites in place and prints
// nothing — both are already at most a short line list, so there is nothing
// to compress. `-d` is the one shape worth a formatter: it prints a real
// unified diff (verified against the darwin/arm64 `gofmt` binary — the
// standard `--- `/`+++ `/`@@` headers, one hunk per changed region), which
// is exactly what filediff already renders, so this delegates wholesale
// rather than forking its context-collapsing rules.
package gofmt

import (
	"context"

	"github.com/synapctx/sctx/internal/adapters/format/filediff"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `gofmt` output.
type Formatter struct {
	diff *filediff.Formatter
}

// New constructs a gofmt Formatter.
func New() *Formatter { return &Formatter{diff: filediff.New()} }

// Descriptor claims all gofmt invocations; hasDiffFlag decides which ones
// this formatter actually has something to do with.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "gofmt"}
}

// hasDiffFlag reports whether -d is present. gofmt's flags are single
// tokens — unlike a getopt-style CLI it never combines short flags — so an
// exact token match is sufficient and never mistakes an unrelated flag or a
// file path for it.
func hasDiffFlag(argv []string) bool {
	for _, a := range argv[1:] {
		if a == "-d" {
			return true
		}
	}
	return false
}

// Aggressive delegates a `-d` invocation's unified diff to filediff; every
// other invocation (`-l`, `-w`, or a bare conformance check) declines, since
// their output is already minimal or empty.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !hasDiffFlag(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return f.diff.Aggressive(ctx, in)
}

// Relaxed mirrors Aggressive: only a `-d` invocation has content worth
// filtering, delegated to filediff's own relaxed tier.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !hasDiffFlag(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return f.diff.Relaxed(ctx, in)
}
