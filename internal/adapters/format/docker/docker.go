// Package docker implements a format.Formatter for the docker CLI. It claims
// every docker invocation and dispatches internally on the subcommand (with
// "compose" handled as a nested subcommand), since the registry holds one
// entry per program.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/nested"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/dockerargv"
)

// Resolver looks up the formatter for a nested command executed in a
// container. It is injected by main to keep this adapter independent of the
// application registry package.
type Resolver = nested.Resolver

// Formatter renders docker command output.
type Formatter struct {
	resolve Resolver
}

// New constructs a docker Formatter.
func New(resolve ...Resolver) *Formatter {
	f := &Formatter{}
	if len(resolve) > 0 {
		f.resolve = resolve[0]
	}
	return f
}

// Descriptor claims all docker invocations; subcommand dispatch happens
// inside Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "docker"}
}

// subcommand returns the docker subcommand (e.g. "ps", or "compose ps") and
// the arguments that follow it. Docker and Compose globals are parsed by the
// shared grammar also used by the hook and stats key.
func subcommand(argv []string) (sub string, rest []string) {
	inv, ok := dockerargv.Parse(argv)
	if !ok {
		return "", nil
	}
	if inv.Command == "compose" {
		nested, ok := dockerargv.ParseCompose(inv)
		if !ok {
			return "compose", nil
		}
		return "compose " + nested.Command, nested.Args
	}
	if inv.Command == "network" || inv.Command == "volume" || inv.Command == "container" || inv.Command == "image" {
		for i, arg := range inv.Args {
			if !strings.HasPrefix(arg, "-") {
				return inv.Command + " " + arg, inv.Args[i+1:]
			}
		}
	}
	return inv.Command, inv.Args
}

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, rest := subcommand(in.Argv)
	if sub == "" || explicitOutputContract(in.Argv, sub, rest) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// A nested command's non-zero exit belongs to that command, not
	// necessarily to Docker. Let its formatter retain and render the failure.
	if sub == "exec" || sub == "compose exec" {
		return f.aggressiveExec(ctx, in, sub, rest)
	}

	// A non-zero exit almost always means an error on stderr that these
	// structured parsers don't understand; degrade to relaxed line
	// filtering, which preserves it.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	switch sub {
	case "ps", "container ls":
		return aggressivePs(in)
	case "images", "image ls":
		return aggressiveImages(in)
	case "logs":
		return aggressiveLogs(in)
	case "build":
		return aggressiveBuild(in)
	case "pull", "push":
		return aggressivePullPush(in)
	case "inspect":
		return aggressiveInspect(ctx, in)
	case "stats":
		return aggressiveStats(in)
	case "history":
		return aggressiveHistory(in)
	case "top":
		return aggressiveTop(in)
	case "network ls":
		return aggressiveNetworkLs(in)
	case "volume ls":
		return aggressiveVolumeLs(in)
	case "compose ps":
		return aggressiveComposePs(in)
	case "compose up":
		return aggressiveComposeUp(in)
	case "compose build":
		return aggressiveBuild(in)
	case "compose logs":
		return aggressiveLogs(in)
	case "compose down":
		return aggressiveComposeDown(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any docker subcommand.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, rest := subcommand(in.Argv)
	if !textSubcommand(sub) || explicitOutputContract(in.Argv, sub, rest) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if sub == "exec" || sub == "compose exec" {
		return f.relaxedExec(ctx, in, sub, rest)
	}
	// Docker/daemon failures are already finite and diagnostic-heavy. Keep
	// their native streams byte-for-byte instead of folding stderr into a
	// human summary merely to claim a relaxed match.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return relaxedFilter(in)
}

func textSubcommand(sub string) bool {
	switch sub {
	case "ps", "container ls", "images", "image ls", "logs", "build", "pull", "push", "inspect", "stats", "history", "top", "network ls", "volume ls",
		"compose ps", "compose up", "compose build", "compose logs", "compose down", "exec", "compose exec":
		return true
	default:
		return false
	}
}

// explicitOutputContract reports modes where stdout is a user-selected data
// contract, identifier stream, or non-human progress format. Those bytes are
// authoritative and bypass both Docker tiers.
func explicitOutputContract(argv []string, sub string, rest []string) bool {
	switch sub {
	case "inspect":
		_, ok := dockerargv.OptionValue(rest, "-f", "--format")
		return ok
	case "ps", "container ls", "images", "image ls", "stats", "history", "network ls", "volume ls", "compose ps":
		if _, ok := dockerargv.OptionValue(rest, "--format"); ok {
			return true
		}
		return dockerargv.HasFlag(rest, "-q", "--quiet")
	case "build", "compose build":
		if dockerargv.HasFlag(rest, "-q", "--quiet") {
			return true
		}
		if progress, ok := dockerargv.OptionValue(rest, "--progress"); ok && (progress == "json" || progress == "quiet" || progress == "rawjson") {
			return true
		}
		// Compose's --progress is a parent option and is not present in rest.
		if top, ok := dockerargv.Parse(argv); ok && top.Command == "compose" {
			if progress, ok := dockerargv.OptionValue(top.Args, "--progress"); ok && (progress == "json" || progress == "quiet" || progress == "rawjson") {
				return true
			}
		}
	}
	return false
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
