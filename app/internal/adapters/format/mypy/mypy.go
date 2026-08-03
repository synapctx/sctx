// Package mypy implements a format.Formatter for mypy (Python static type
// checker) output. mypy has no subcommands worth dispatching on: the
// formatter claims the bare "mypy" program and applies structured formatting
// to its default diagnostic report.
package mypy

import (
	"bufio"
	"bytes"
	"context"
	"io"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders mypy command output.
type Formatter struct{}

// New constructs a mypy Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all mypy invocations; mypy has no subcommands to
// dispatch on.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "mypy"}
}

// Aggressive parses mypy's default diagnostic report into a per-file grouped
// listing.
//
// mypy exits 1 when type errors are found: that is the normal case, not an
// error to degrade on. Only an unparseable output (no diagnostics and no
// recognized summary line) degrades to the next tier.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return aggressiveReport(in)
}

// Relaxed applies heuristic line-level filtering to any mypy invocation.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return relaxedFilter(in)
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

// splitLines splits raw bytes into lines without trailing newlines, dropping
// a single final empty element caused by a trailing newline.
func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
