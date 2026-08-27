// Package gotest formats output from the `go` CLI (test, build, vet, and
// other subcommands) into a token-minimal rendering. It claims every `go`
// invocation and dispatches internally on the subcommand rather than
// registering one formatter per subcommand.
package gotest

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `go` command output for the aggressive and relaxed
// tiers. It is safe for concurrent use; it holds no mutable state.
type Formatter struct{}

// New constructs a Formatter for the `go` CLI.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all `go` invocations; subcommand routing happens inside
// Aggressive/Relaxed.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "go"}
}

// Aggressive renders a structured, maximally compressed summary for `go
// test`, and an error-focused summary for `go build`/`go vet`. It also
// covers `go mod`, `go list`, `go run`, `go generate`, `go get`, and `go fix`
// (apply mode only). Any other go subcommand is not handled at this tier.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	switch subcommand(in) {
	case "test":
		if inspectionMode(in) {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveTest(in)
	case "build", "vet":
		return aggressiveBuildVet(in, subcommand(in))
	case "mod":
		return aggressiveMod(in)
	case "list":
		return aggressiveList(ctx, in)
	case "run":
		return aggressiveRun(in)
	case "generate":
		return aggressiveGenerate(in)
	case "get":
		return aggressiveGet(in)
	case "fix":
		// -diff emits a patch; see diffMode for why no tier may touch it.
		if diffMode(in) {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveFix(in)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

// Relaxed applies generic line-level noise filtering to any `go` subcommand:
// collapsing repeated lines, dropping progress noise, and retaining error
// signal.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if subcommand(in) == "test" && inspectionMode(in) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// A `go fix -diff` patch must reach the caller byte-exact: relaxedFilter
	// drops blank context lines and collapses repeated ones, both of which
	// change what the patch says. Decline so it falls through to verbatim.
	if subcommand(in) == "fix" && diffMode(in) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return relaxedRender(in)
}

// inspectionModeFlags are `go test` flags that replace the test-run output with
// something else entirely. Their output has no test events and no Benchmark lines, so
// the test renderer summarises an empty run and DISCARDS the actual answer.
//
// Measured: `go test -list 'Test.*' ./internal/adapters/hook/` prints six test names
// raw, and rendered as "ok: 1 packages, 444ms". An agent listing tests to decide what to
// run would conclude there are none — the command's entire purpose, silently deleted.
//
// Flag-based rather than output-shape-based, unlike the benchmark tier which sniffs for
// "Benchmark" lines. A list of bare test names is not distinguishable from other plain
// output, whereas the flag is an unambiguous statement that the output contract has
// changed.
var inspectionModeFlags = map[string]bool{
	"-list": true, "--list": true, // prints matching test names
	"-n": true, "--n": true, // prints the commands it would run
	"-h": true, "--help": true, // usage text
}

// inspectionMode reports whether this `go test` invocation is an inspection rather than
// a run, in which case every tier must decline and let the raw output through.
func inspectionMode(in format.Input) bool {
	for _, a := range in.Argv {
		// Both `-list X` and `-list=X` forms.
		if i := strings.IndexByte(a, '='); i > 0 {
			a = a[:i]
		}
		if inspectionModeFlags[a] {
			return true
		}
	}
	return false
}

// subcommand extracts the go subcommand (e.g. "test", "build", "vet") from
// the normalized Command key, falling back to scanning Argv for the first
// non-flag token when Command doesn't carry it.
func subcommand(in format.Input) string {
	if fields := strings.Fields(in.Command); len(fields) >= 2 {
		return fields[1]
	}
	for _, a := range in.Argv {
		if a == "" || a == "go" || strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// readAll drains r fully, tolerating a nil reader (treated as empty).
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// splitLines splits text on newlines without leaving a trailing empty
// element for a final "\n".
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// dedupeLines removes lines that repeat verbatim anywhere earlier in the
// slice, preserving first-occurrence order.
func dedupeLines(lines []string) []string {
	seen := make(map[string]bool, len(lines))
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}
