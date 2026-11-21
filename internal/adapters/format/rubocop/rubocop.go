// Package rubocop formats `rubocop` offense reports: offenses grouped by file
// with the source/caret echo removed, identical cop+message offenses folded
// into one line of positions, and a cop histogram. No offense is dropped.
package rubocop

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `rubocop` output. It holds no state.
type Formatter struct{}

// New constructs a Formatter for `rubocop`.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `rubocop` invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "rubocop"} }

// Aggressive groups the offense report by file.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	return aggressiveRender(stdout, stderr)
}

// Relaxed drops blank lines and the progress row and collapses repeats.
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

// plainFormats are the human text formatters this package understands.
var plainFormats = map[string]bool{
	"progress": true, "p": true, "simple": true, "s": true,
}

// machineFormat reports whether argv selects a formatter other than the
// progress/simple text ones (json, junit, github, html, custom...) or a
// listing/cache command whose output is not an offense report.
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
		case a == "--show-cops" || a == "--version" || a == "-v" || a == "--help" || a == "-h" ||
			a == "-L" || a == "--list-target-files" || a == "-V":
			return true
		default:
			continue
		}
		if !plainFormats[v] {
			return true
		}
	}
	return false
}

func readStreams(in format.Input) (stdout, stderr string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return "", "", fmt.Errorf("rubocop: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("rubocop: reading stderr: %w", err)
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
