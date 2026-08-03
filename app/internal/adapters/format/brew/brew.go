// Package brew implements a format.Formatter for the Homebrew CLI, covering
// `brew install` and `brew upgrade`. It claims every brew invocation (the
// registry holds one entry per program) and dispatches internally, applying
// structured compression only to install/upgrade and degrading everything
// else straight to ErrTierInapplicable at the Aggressive tier.
package brew

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders brew install/upgrade output.
type Formatter struct{}

// New constructs a brew Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all brew invocations; subcommand dispatch happens inside
// Aggressive/Relaxed since the registry holds one entry per program.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "brew"}
}

// subcommand returns "install" or "upgrade" if that is the brew subcommand
// being invoked (skipping leading flags), or "" for anything else (e.g.
// brew list, brew search) that this formatter does not structurally cover.
func subcommand(argv []string) string {
	if len(argv) <= 1 {
		return ""
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "install" || a == "upgrade" {
			return a
		}
		return ""
	}
	return ""
}

// Aggressive structurally compresses `brew install`/`brew upgrade` output;
// any other subcommand degrades to the next tier.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub := subcommand(in.Argv)
	if sub != "install" && sub != "upgrade" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return aggressiveInstallUpgrade(in)
}

// Relaxed applies heuristic line-level filtering to any brew invocation.
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
