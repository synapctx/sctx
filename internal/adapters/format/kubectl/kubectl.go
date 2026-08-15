// Package kubectl implements a format.Formatter for the kubectl CLI. It
// claims every kubectl invocation and dispatches internally on the
// subcommand, since the registry holds one entry per program.
package kubectl

import (
	"bufio"
	"bytes"
	"context"
	"io"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/kubectlargv"
)

// Resolver reports which formatter handles a nested kubectl exec command.
type Resolver func(argv []string) (format.Formatter, bool)

// Formatter renders kubectl command output.
type Formatter struct {
	resolve Resolver
}

// New constructs a kubectl Formatter. The optional resolver enables safe
// delegation for non-interactive `kubectl exec -- COMMAND` invocations.
func New(resolve ...Resolver) *Formatter {
	f := &Formatter{}
	if len(resolve) > 0 {
		f.resolve = resolve[0]
	}
	return f
}

// Descriptor claims all kubectl invocations; subcommand dispatch happens
// inside Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "kubectl"}
}

// subcommand returns the kubectl subcommand (e.g. "get") and the arguments
// that follow it, skipping argv[0] and any global flags (and their values).
func subcommand(argv []string) (sub string, rest []string) {
	inv, ok := kubectlargv.Parse(argv)
	if !ok {
		return "", nil
	}
	return inv.Command, inv.Args
}

// outputFormat scans command-local args for a -o/--output value. It stops at
// `--`, so a nested exec command's own -o flag is never attributed to kubectl.
func outputFormat(args []string) string {
	value, ok := kubectlargv.OptionValue(args, "-o", "--output")
	if !ok {
		return ""
	}
	return value
}

// getEventsAliases are the resource-type spellings that route `kubectl get
// <resource>` to the events table renderer instead of the default get
// renderer.
var getEventsAliases = map[string]bool{"events": true, "event": true, "ev": true}

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, rest := subcommand(in.Argv)
	if sub == "exec" {
		return f.aggressiveExec(ctx, in, rest)
	}

	// A non-zero exit almost always means an error on stderr that these
	// structured parsers don't understand; degrade to relaxed line
	// filtering, which preserves it.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	outFmt := outputFormat(rest)
	if outFmt == "json" {
		return aggressiveJSON(ctx, in)
	}

	switch sub {
	case "get":
		switch outFmt {
		case "":
			if _, raw := kubectlargv.OptionValue(rest, "--raw"); raw {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			if kubectlargv.HasFlag(rest, "--no-headers") {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			if len(rest) > 0 && getEventsAliases[rest[0]] {
				return aggressiveEvents(in)
			}
			if kubectlargv.HasFlag(rest, "--show-labels", "-L", "--label-columns") {
				return aggressiveGetWide(in)
			}
			if _, sorted := kubectlargv.OptionValue(rest, "--sort-by"); sorted {
				return aggressiveGetWide(in)
			}
			return aggressiveGet(in)
		case "wide":
			return aggressiveGetWide(in)
		default:
			// yaml, name, custom-columns, jsonpath, ...: leave to another
			// tier rather than risk misparsing a non-table format.
			return format.Rendered{}, format.ErrTierInapplicable
		}
	case "describe":
		return aggressiveDescribe(in)
	case "logs":
		return aggressiveLogs(in)
	case "top":
		return aggressiveTop(in, rest)
	case "events":
		return aggressiveEvents(in)
	case "rollout":
		return aggressiveRollout(in, rest)
	case "api-resources":
		return aggressiveAPIResources(in)
	case "apply", "create", "delete", "patch", "scale", "label", "annotate":
		return aggressiveResultLines(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any kubectl subcommand.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, rest := subcommand(in.Argv)
	if sub == "exec" {
		return f.relaxedExec(ctx, in, rest)
	}
	if outputFormat(rest) != "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if sub == "get" {
		if _, raw := kubectlargv.OptionValue(rest, "--raw"); raw {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
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
