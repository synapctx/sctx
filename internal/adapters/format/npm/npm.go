// Package npm implements a format.Formatter for the npm, pnpm, and yarn
// JavaScript package-manager CLIs. It claims each program name and
// dispatches internally on the subcommand (install, run, audit, list, ...),
// since the registry holds one entry per program. All() mirrors the
// pip/pip3 pattern: three thin instances sharing the same internals.
package npm

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders npm/pnpm/yarn command output.
type Formatter struct {
	command string
}

// New returns the npm formatter.
func New() format.Formatter { return &Formatter{command: "npm"} }

// All returns the npm, pnpm, and yarn formatters, sharing internals.
func All() []format.Formatter {
	return []format.Formatter{
		&Formatter{command: "npm"},
		&Formatter{command: "pnpm"},
		&Formatter{command: "yarn"},
	}
}

// Descriptor claims all invocations of the formatter's program name;
// subcommand dispatch happens inside Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

// installSubcommands are the variants of "install this package" that share
// the same noisy shape: progress, deprecation warnings, funding nags, then a
// terse summary line.
var installSubcommands = map[string]bool{
	"install": true,
	"i":       true,
	"ci":      true,
	"add":     true,
	"update":  true,
}

// listSubcommands render columnar/tree package listings.
var listSubcommands = map[string]bool{
	"list":     true,
	"ls":       true,
	"outdated": true,
}

// runSubcommands shell out to a package.json script or an arbitrary binary;
// only the package manager's own wrapper lines are noise.
var runSubcommands = map[string]bool{
	"run":  true,
	"test": true,
	"exec": true,
}

// subcommand returns the first non-flag argument after argv[0], which is
// npm/pnpm/yarn's subcommand (e.g. "install", "run"). Flags of the form
// --key=value or bare -x never consume the next slot for these CLIs' top
// level dispatch, so no value-flag table is needed here.
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

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub := subcommand(in.Argv)

	switch {
	case installSubcommands[sub]:
		return aggressiveInstall(in)
	case sub == "audit":
		return aggressiveAudit(in)
	case listSubcommands[sub]:
		return aggressiveList(in)
	case runSubcommands[sub]:
		return aggressiveRun(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any npm/pnpm/yarn
// invocation.
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

// isErrorLine reports whether t (already trimmed) is an npm/pnpm/yarn error
// signal that must always survive verbatim.
func isErrorLine(t string) bool {
	switch {
	case strings.HasPrefix(t, "npm ERR!"),
		strings.HasPrefix(t, "npm error"),
		strings.Contains(t, "ERR_PNPM"),
		strings.HasPrefix(t, "error "),
		strings.HasPrefix(t, "error:"):
		return true
	default:
		return false
	}
}

// isDeprecationLine reports whether t is a deprecation warning, capped
// separately from other noise since it can carry actionable signal.
func isDeprecationLine(t string) bool {
	return strings.Contains(strings.ToLower(t), "deprecat")
}

// isFundingLine reports whether t is npm's funding nag, always noise.
func isFundingLine(t string) bool {
	return strings.Contains(t, "looking for funding") || strings.Contains(t, "npm fund")
}

// isWrapperLine reports whether t is npm/yarn's own script-invocation echo
// printed before `run`/`test`/`exec` hands off to the underlying script,
// e.g. "> pkg@1.0.0 test" / "> jest ./test".
func isWrapperLine(t string) bool {
	return strings.HasPrefix(t, "> ")
}
