// Package makefmt implements a format.Formatter for GNU make. It collapses
// directory-entering/leaving noise from recursive make and repeated recipe
// echoes, while always keeping error/warning signal and non-zero-exit
// output intact.
package makefmt

import (
	"bufio"
	"bytes"
	"context"
	"io"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders make command output.
type Formatter struct{}

// New constructs a make Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all make invocations.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "make"}
}

// Aggressive classifies make output into directory noise, error/warning
// signal, and duplicate recipe echoes; see aggressiveMake for the rules.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return aggressiveMake(in)
}

// Relaxed applies heuristic line-level filtering to any make invocation.
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
