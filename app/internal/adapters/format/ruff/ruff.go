// Package ruff implements a format.Formatter for ruff, the Python
// linter/formatter. It claims every ruff invocation and dispatches
// internally on the subcommand ("check" vs "format"), since the two have
// unrelated output shapes.
package ruff

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders ruff command output.
type Formatter struct{}

// New constructs a ruff Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all ruff invocations; subcommand dispatch happens
// inside Aggressive.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "ruff"}
}

// subcommand returns the ruff subcommand (e.g. "check", "format"), skipping
// argv[0] and any leading flags.
func subcommand(argv []string) string {
	if len(argv) <= 1 {
		return ""
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// Aggressive parses `ruff check` or `ruff format` output into a compact
// rendering.
//
// ruff check exits 1 when lint diagnostics are found: that is the normal
// case, not an error to degrade on. Only exit codes above 1 indicate a real
// failure (bad config, crash), which must degrade to preserve the error
// signal.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	switch subcommand(in.Argv) {
	case "check", "":
		if in.ExitCode > 1 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveCheck(in)
	case "format":
		if in.ExitCode > 1 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveFormat(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any ruff invocation.
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
