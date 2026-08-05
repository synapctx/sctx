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

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders docker command output.
type Formatter struct{}

// New constructs a docker Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all docker invocations; subcommand dispatch happens
// inside Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "docker"}
}

// dockerGlobalFlagsWithValue are docker's own global options that consume
// the next argv slot, so they must not be mistaken for the subcommand.
// -D/--debug is bare and needs no entry here.
var dockerGlobalFlagsWithValue = map[string]bool{
	"--context":   true,
	"-H":          true,
	"--host":      true,
	"--config":    true,
	"-l":          true,
	"--log-level": true,
}

// nestedSubcommands are docker's own "object subcommand" groups: the token
// after them is itself a subcommand (e.g. "network ls", "compose ps") rather
// than an argument.
var nestedSubcommands = map[string]bool{
	"compose":   true,
	"network":   true,
	"volume":    true,
	"container": true,
}

// subcommand returns the docker subcommand (e.g. "ps", or "compose ps") and
// the arguments that follow it, skipping argv[0] and any global flags (and
// their values).
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
			if dockerGlobalFlagsWithValue[a] {
				i++
			}
			continue
		}
		if nestedSubcommands[a] {
			for j := i + 1; j < len(args); j++ {
				b := args[j]
				if strings.HasPrefix(b, "-") {
					continue
				}
				return a + " " + b, args[j+1:]
			}
			return a, nil
		}
		return a, args[i+1:]
	}
	return "", nil
}

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, _ := subcommand(in.Argv)

	// A non-zero exit almost always means an error on stderr that these
	// structured parsers don't understand; degrade to relaxed line
	// filtering, which preserves it.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	switch sub {
	case "ps", "container ls":
		return aggressivePs(in)
	case "images":
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
