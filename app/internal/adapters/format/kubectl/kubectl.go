// Package kubectl implements a format.Formatter for the kubectl CLI. It
// claims every kubectl invocation and dispatches internally on the
// subcommand, since the registry holds one entry per program.
package kubectl

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders kubectl command output.
type Formatter struct{}

// New constructs a kubectl Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all kubectl invocations; subcommand dispatch happens
// inside Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "kubectl"}
}

// kubectlGlobalFlagsWithValue are kubectl's own global options that consume
// the next argv slot, so they must not be mistaken for the subcommand.
var kubectlGlobalFlagsWithValue = map[string]bool{
	"-n":           true,
	"--namespace":  true,
	"--context":    true,
	"--kubeconfig": true,
	"-o":           true,
	"--output":     true,
}

// subcommand returns the kubectl subcommand (e.g. "get") and the arguments
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
			if kubectlGlobalFlagsWithValue[a] {
				i++
			}
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// outputFormat scans args (argv without argv[0]) for a -o/--output value,
// regardless of its position relative to the subcommand.
func outputFormat(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "-o="):
			return strings.TrimPrefix(a, "-o=")
		case strings.HasPrefix(a, "--output="):
			return strings.TrimPrefix(a, "--output=")
		case a == "-o" || a == "--output":
			if i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

// getEventsAliases are the resource-type spellings that route `kubectl get
// <resource>` to the events table renderer instead of the default get
// renderer.
var getEventsAliases = map[string]bool{"events": true, "event": true, "ev": true}

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, rest := subcommand(in.Argv)

	// A non-zero exit almost always means an error on stderr that these
	// structured parsers don't understand; degrade to relaxed line
	// filtering, which preserves it.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	switch sub {
	case "get":
		outFmt := ""
		if len(in.Argv) > 1 {
			outFmt = outputFormat(in.Argv[1:])
		}
		switch outFmt {
		case "":
			if len(rest) > 0 && getEventsAliases[rest[0]] {
				return aggressiveEvents(in)
			}
			return aggressiveGet(in)
		case "json":
			return aggressiveGetJSON(ctx, in)
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
		return aggressiveTop(in)
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
