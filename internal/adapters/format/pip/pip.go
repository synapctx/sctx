// Package pip implements a format.Formatter for pip/pip3 output. It claims
// the pip/pip3 program name and dispatches internally on the subcommand
// (install, list, freeze, show, ...), since the registry holds one entry per
// program. pip and pip3 are exposed as two thin instances (All()) sharing
// the same internals, mirroring the cat/head/tail split in package read.
package pip

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"slices"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders pip/pip3 command output.
type Formatter struct {
	command string
}

// New returns the pip formatter.
func New() format.Formatter { return &Formatter{command: "pip"} }

// All returns the pip and pip3 formatters, sharing internals.
func All() []format.Formatter {
	return []format.Formatter{
		&Formatter{command: "pip"},
		&Formatter{command: "pip3"},
	}
}

// Descriptor claims all invocations of the formatter's program name;
// subcommand dispatch happens inside Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

// pipValueFlags are pip's own global options that consume the next argv
// slot, so they must not be mistaken for the subcommand.
var pipValueFlags = map[string]bool{
	"--python":          true,
	"--cache-dir":       true,
	"--log":             true,
	"--proxy":           true,
	"--retries":         true,
	"--timeout":         true,
	"--exists-action":   true,
	"--trusted-host":    true,
	"--index-url":       true,
	"--extra-index-url": true,
}

// subcommand returns the pip subcommand (e.g. "install") and the arguments
// that follow it, skipping argv[0] and any global flags (and their values).
func subcommand(argv []string) (sub string, rest []string) {
	if len(argv) <= 1 {
		return "", nil
	}
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			// Flags of the form --key=value never consume the next slot.
			if strings.Contains(a, "=") {
				continue
			}
			if pipValueFlags[a] {
				i++
			}
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// isFreezeFormat reports whether `pip list` was invoked with a freeze-style
// output format, which renders as bare "pkg==version" lines rather than a
// two-column table.
func isFreezeFormat(rest []string) bool {
	return slices.Contains(rest, "--format=freeze")
}

// Aggressive dispatches to a per-subcommand structured renderer. Unlike most
// formatters, "install" is dispatched before the exit-code check: it is
// expected to fail often (unresolvable dependencies, network errors) and
// still needs a compact rendering that preserves the ERROR lines fully.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, rest := subcommand(in.Argv)

	switch sub {
	case "install":
		return aggressiveInstall(in)
	case "list", "freeze":
		// A non-zero exit here almost always means an error on stderr that
		// these structured parsers don't understand; degrade to relaxed
		// line filtering, which preserves it.
		if in.ExitCode != 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveList(in, sub == "freeze" || isFreezeFormat(rest))
	case "show":
		if in.ExitCode != 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveShow(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any pip subcommand.
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
