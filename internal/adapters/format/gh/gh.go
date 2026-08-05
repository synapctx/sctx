// Package gh implements a format.Formatter for the GitHub CLI (gh). It
// claims every gh invocation and dispatches internally on the first two
// non-flag arguments (e.g. "pr", "list"), since the registry holds one entry
// per program.
package gh

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders gh command output.
type Formatter struct{}

// New constructs a gh Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all gh invocations; subcommand dispatch happens inside
// Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "gh"}
}

// ghGlobalFlagsWithValue are gh's own global options that consume the next
// argv slot, so they must not be mistaken for a subcommand.
var ghGlobalFlagsWithValue = map[string]bool{
	"-R":         true,
	"--repo":     true,
	"--hostname": true,
	"--json":     true,
	"--jq":       true,
	"-t":         true,
	"--template": true,
}

// subcommand returns up to two levels of gh subcommand (e.g. "pr", "list")
// and the arguments that follow, skipping argv[0] and any global flags (and
// their values).
func subcommand(argv []string) (level1, level2 string, rest []string) {
	if len(argv) <= 1 {
		return "", "", nil
	}
	args := argv[1:]
	var levels []string
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Flags of the form --key=value never consume the next slot.
			if strings.Contains(a, "=") {
				continue
			}
			if ghGlobalFlagsWithValue[a] {
				i++
			}
			continue
		}
		levels = append(levels, a)
		if len(levels) == 2 {
			i++
			break
		}
	}
	switch len(levels) {
	case 0:
		return "", "", args
	case 1:
		return levels[0], "", args[i:]
	default:
		return levels[0], levels[1], args[i:]
	}
}

// hasJSONOutput reports whether --json or --jq appears anywhere in argv,
// meaning gh is already emitting machine-readable output that structured
// aggressive formatting must not butcher.
func hasJSONOutput(argv []string) bool {
	for _, a := range argv {
		if a == "--json" || a == "--jq" || strings.HasPrefix(a, "--json=") || strings.HasPrefix(a, "--jq=") {
			return true
		}
	}
	return false
}

// Aggressive dispatches to a per-subcommand structured renderer.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if hasJSONOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// A non-zero exit almost always means an error message these structured
	// parsers don't understand; degrade to relaxed line filtering, which
	// preserves it.
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	l1, l2, _ := subcommand(in.Argv)
	switch {
	case l1 == "pr" && l2 == "list":
		return aggressiveList(in, "pull requests")
	case l1 == "issue" && l2 == "list":
		return aggressiveList(in, "issues")
	case l1 == "run" && l2 == "list":
		return aggressiveRunList(in)
	case (l1 == "pr" || l1 == "issue") && l2 == "view":
		return aggressiveView(in)
	case l1 == "pr" && l2 == "checks":
		return aggressiveChecks(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies heuristic line-level filtering to any gh subcommand.
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
