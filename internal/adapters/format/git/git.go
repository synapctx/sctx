// Package git implements a format.Formatter for the git CLI. It claims every
// git invocation and dispatches internally on the first non-flag argument
// (the git subcommand), since the registry holds one entry per program.
package git

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders git command output.
type Formatter struct{}

// New constructs a git Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all git invocations; subcommand dispatch happens inside
// Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "git"}
}

// gitGlobalFlagsWithValue are git's own global options that consume the next
// argv slot, so they must not be mistaken for the subcommand.
var gitGlobalFlagsWithValue = map[string]bool{
	"-C":           true,
	"-c":           true,
	"--git-dir":    true,
	"--work-tree":  true,
	"--namespace":  true,
	"--exec-path":  true,
	"--config-env": true,
}

// subcommand returns the git subcommand (e.g. "status") and the arguments
// that follow it, skipping argv[0] and any global flags (and their values).
func subcommand(argv []string) (sub string, rest []string) {
	if len(argv) <= 1 {
		return "", nil
	}
	args := argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Flags of the form --key=value never consume the next slot.
			if strings.Contains(a, "=") {
				continue
			}
			if gitGlobalFlagsWithValue[a] {
				i++
			}
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, args := subcommand(in.Argv)

	switch sub {
	case "add", "commit", "push", "pull", "fetch":
		return aggressiveWrite(sub, in)
	}

	// For read-only/reporting subcommands, a non-zero exit almost always
	// means an error message on stderr that these structured parsers don't
	// understand; degrade to relaxed line filtering, which preserves it.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	switch sub {
	case "status":
		return aggressiveStatus(in)
	case "log":
		return aggressiveLog(in, args)
	case "diff", "show":
		return aggressiveDiff(in)
	case "branch":
		return aggressiveBranch(in)
	case "stash":
		return aggressiveStash(in, args)
	case "blame":
		return aggressiveBlame(in)
	case "reflog":
		return aggressiveReflog(in)
	case "tag":
		return aggressiveTag(in)
	case "remote":
		return aggressiveRemote(in, args)
	case "shortlog":
		return aggressiveShortlog(in)
	case "ls-files":
		return aggressiveLsFiles(in)
	case "worktree":
		return aggressiveWorktree(in, args)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any git subcommand.
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
