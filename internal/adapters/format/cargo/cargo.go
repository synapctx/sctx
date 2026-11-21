// Package cargo formats output from the `cargo` CLI. It claims every `cargo`
// invocation and dispatches internally: test, build, check, clippy, doc, run
// (build part only), install, fmt --check and nextest run get dedicated
// rendering; every other subcommand declines. cargo writes
// progress and diagnostics to stderr and test-harness output to stdout, so
// both streams are scanned and folded into one body.
package cargo

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders cargo command output.
type Formatter struct{}

// New constructs a cargo Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims all cargo invocations.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "cargo"} }

// Aggressive renders failures-first summaries for test/build/check/clippy.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub := dispatch(in.Argv)
	if !handled(sub) || inspection(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return render(in, sub)
}

// Relaxed drops only progress and passing-test lines; everything else is kept.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub := dispatch(in.Argv)
	if !handled(sub) || inspection(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return renderRelaxed(in, sub)
}

func handled(sub string) bool {
	switch sub {
	case "test", "build", "check", "clippy", "doc", "run", "install", "fmt", "nextest":
		return true
	}
	return false
}

// globalValueFlags are cargo options that take a separate value before the
// subcommand; the value must not be mistaken for the subcommand.
var globalValueFlags = map[string]bool{"--manifest-path": true, "-C": true, "--config": true, "-Z": true, "--color": true}

// subcommand returns the first non-flag, non-toolchain argument after argv[0].
func subcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if globalValueFlags[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			continue
		}
		return a
	}
	return ""
}

// dispatch is the subcommand the formatter routes on: "nextest" only for
// `cargo nextest run`; other nextest verbs (list, archive) return "nextest-other".
func dispatch(argv []string) string {
	sub := subcommand(argv)
	if sub != "nextest" {
		return sub
	}
	seen := false
	for _, a := range argv[1:] {
		if a == "--" {
			break
		}
		if seen && !strings.HasPrefix(a, "-") {
			if a == "run" || a == "r" {
				return "nextest"
			}
			return "nextest-other"
		}
		if a == "nextest" {
			seen = true
		}
	}
	return "nextest-other"
}

// inspection reports flags that change the output contract away from the
// human-readable text (JSON messages, test listing, help).
func inspection(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	for _, a := range argv[1:] {
		if before, _, ok := strings.Cut(a, "="); ok {
			a = before
		}
		switch a {
		case "--message-format", "--list", "--format", "-h", "--help":
			return true
		}
	}
	return false
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
