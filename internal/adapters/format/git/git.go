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

	"github.com/synapctx/sctx/internal/adapters/format/generic"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/gitargv"
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

// Dedicated reports whether argv selects one of Git's purpose-built renderers.
// Finite unknown verbs still pass through this formatter for the generic safety
// net, but accounting must not call that dedicated command coverage.
func (f *Formatter) Dedicated(argv []string) bool {
	sub, _ := subcommand(argv)
	switch sub {
	case "add", "commit", "push", "pull", "fetch", "status", "log", "diff", "show",
		"branch", "stash", "blame", "reflog", "tag", "remote", "shortlog", "ls-files", "worktree":
		return true
	default:
		return false
	}
}

// subcommand returns the git subcommand (e.g. "status") and the arguments
// that follow it, using the same global-option grammar as the hook.
func subcommand(argv []string) (sub string, rest []string) {
	inv, ok := gitargv.Parse(argv)
	if !ok {
		return "", nil
	}
	return inv.Command, inv.Args
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
	// understand; both tiers decline so the chain preserves it verbatim.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	switch sub {
	case "status":
		return aggressiveStatus(in, args)
	case "log":
		return aggressiveLog(in, args)
	case "diff", "show":
		return aggressiveDiff(in, args)
	case "branch":
		return aggressiveBranch(in, args)
	case "stash":
		return aggressiveStash(in, args)
	case "blame":
		return aggressiveBlame(in)
	case "reflog":
		return aggressiveReflog(in, args)
	case "tag":
		return aggressiveTag(in, args)
	case "remote":
		return aggressiveRemote(in, args)
	case "shortlog":
		return aggressiveShortlog(in, args)
	case "ls-files":
		return aggressiveLsFiles(in, args)
	case "worktree":
		return aggressiveWorktree(in, args)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies Git-specific filtering to dedicated human output and the
// shape-only generic safety net to unknown finite commands.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 || machineReadableInvocation(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	sub, args := subcommand(in.Argv)
	if opaqueOutputInvocation(sub, args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if !dedicatedHumanOutput(sub, args) {
		// Unknown finite Git verbs still get the shape-only generic safety net:
		// valid JSON compaction and provable repeated-line collapsing. No output
		// grammar is guessed, so this is reachability rather than dedicated coverage.
		return generic.New().Relaxed(ctx, in)
	}
	return relaxedFilter(in)
}

func opaqueOutputInvocation(sub string, args []string) bool {
	switch sub {
	case "cat-file", "archive", "config", "notes":
		return true
	case "show":
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && strings.Contains(arg, ":") {
				return true
			}
		}
	}
	return false
}

func dedicatedHumanOutput(sub string, args []string) bool {
	switch sub {
	case "add", "commit", "push", "pull", "fetch", "status", "log", "diff",
		"branch", "stash", "blame", "reflog", "tag", "remote", "shortlog",
		"ls-files", "worktree":
		return true
	case "show":
		// `git show REV:path` is arbitrary blob content, not Git presentation.
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && strings.Contains(arg, ":") {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// machineReadableInvocation keeps explicit record separators and user-defined
// formats byte-authoritative. Their contents have no stable line grammar.
func machineReadableInvocation(argv []string) bool {
	sub, args := subcommand(argv)
	if gitargv.HasFlag(args, "-z", "--null") {
		return true
	}
	if gitargv.HasOptionPrefix(args, "--format", "--pretty", "--output", "--template") {
		return true
	}
	switch sub {
	case "blame":
		return hasAnyArg(args, "--porcelain", "--line-porcelain", "--incremental")
	case "ls-files":
		return hasAnyArg(args, "--stage", "-s", "--debug", "--eol", "--unmerged", "-u")
	case "worktree":
		return len(args) > 0 && args[0] == "list" && hasAnyArg(args[1:], "--porcelain")
	default:
		return false
	}
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
