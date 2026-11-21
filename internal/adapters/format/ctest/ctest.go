// Package ctest formats `ctest` output: passing tests collapse into a count,
// failed / timed-out / not-run tests, their captured output, and the summary
// pass through verbatim.
package ctest

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/cc"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	headerRE = regexp.MustCompile(`^Test project `)
	startRE  = regexp.MustCompile(`^\s+Start\s+\d+: `)
	passRE   = regexp.MustCompile(`^\s*\d+/\d+ Test\s+#\d+: .*?\s\.+\s+Passed\s`)
)

// Formatter renders ctest output.
type Formatter struct{}

// New constructs a ctest Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every ctest invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "ctest"} }

// Aggressive drops the header, Start and Passed lines.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed behaves like Aggressive: ctest's line classes are unambiguous, so
// there is no coarser tier to fall back on.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

func render(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv[1:] {
		switch a {
		case "--version", "-N", "--show-only", "--help", "-h", "--help-command", "--print-labels":
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.HasPrefix(a, "--show-only") || strings.HasPrefix(a, "--help") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	rawOut, rawErr := cc.ReadAll(in.Stdout), cc.ReadAll(in.Stderr)
	lines := append(cc.SplitLines(rawOut), cc.SplitLines(rawErr)...)
	var out []string
	var passed, starts, header int
	for _, l := range lines {
		switch {
		case passRE.MatchString(l):
			passed++
		case startRE.MatchString(l):
			starts++
		case headerRE.MatchString(l):
			header++
		default:
			out = append(out, l)
		}
	}
	if passed+starts+header == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Blank lines separate Start blocks in --output-on-failure mode; collapse
	// runs so the dropped Start lines do not leave stranded gaps.
	out = squeezeBlank(out)
	marker := fmt.Sprintf("…+%d lines (%d passed tests, %d Start, %d header)", passed+starts+header, passed, starts, header)
	body := strings.Join(append([]string{marker}, out...), "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func squeezeBlank(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
			continue
		}
		out = append(out, l)
	}
	return out
}
