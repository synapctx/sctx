// Package rspec formats `rspec` output: failures with their messages, diffs
// and spec-side backtrace frames, a one-line rerun command, and the example
// summary. Progress dots, timing and passing-example trees are dropped.
package rspec

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `rspec` output. It holds no state.
type Formatter struct{}

// New constructs a Formatter for `rspec`.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `rspec` invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "rspec"} }

// Aggressive renders a failures-first summary of a run.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	return aggressiveRender(in.Argv, stdout, stderr)
}

// Relaxed collapses blank lines, progress rows and repeated lines.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	return relaxedRender(stdout, stderr)
}

// plainFormats are the formatters whose human text this package understands.
var plainFormats = map[string]bool{
	"progress": true, "p": true, "documentation": true, "d": true,
}

// machineFormat reports whether argv selects a formatter other than the
// progress/documentation text ones (json, junit, html, custom classes): those
// are contracts a caller parses, so they must pass through untouched.
func machineFormat(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		var v string
		switch {
		case a == "--format" || a == "-f":
			if i+1 >= len(argv) {
				return true
			}
			v = argv[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			v = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "-f") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			v = a[2:]
		default:
			continue
		}
		if !plainFormats[v] {
			return true
		}
	}
	return false
}

func docFormat(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--format=documentation", a == "--format=d", a == "-fd", a == "-fdocumentation":
			return true
		case (a == "--format" || a == "-f") && i+1 < len(argv) && (argv[i+1] == "documentation" || argv[i+1] == "d"):
			return true
		}
	}
	return false
}

func readStreams(in format.Input) (stdout, stderr string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return "", "", fmt.Errorf("rspec: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("rspec: reading stderr: %w", err)
	}
	return string(o), string(e), nil
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

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
