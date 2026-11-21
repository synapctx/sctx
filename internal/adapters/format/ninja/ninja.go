// Package ninja formats `ninja` build output: the "[N/M] Building ..." step
// lines collapse into a counted marker, the command line printed under a
// FAILED: line is truncated with an explicit marker, and compiler diagnostics
// are filtered by the cc package. Verbose (-v) and tool (-t) invocations ask
// for the very commands this formatter would drop, so they decline.
package ninja

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/cc"
	"github.com/synapctx/sctx/internal/domain/format"
)

var stepRE = regexp.MustCompile(`^\[(\d+)/(\d+)\] `)

const maxCommand = 100

// Formatter renders ninja output.
type Formatter struct{}

// New constructs a ninja Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every ninja invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "ninja"} }

// Aggressive collapses steps, truncates the failed command and filters
// compiler diagnostics.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed collapses steps only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, diagnostics bool) (format.Rendered, error) {
	for _, a := range in.Argv[1:] {
		switch a {
		case "-v", "--verbose", "-t", "--version", "-h", "--help", "-n":
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.HasPrefix(a, "-t") && len(a) > 2 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	rawOut, rawErr := cc.ReadAll(in.Stdout), cc.ReadAll(in.Stderr)
	lines := append(cc.SplitLines(rawOut), cc.SplitLines(rawErr)...)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	changed := false
	var st cc.Stats
	if diagnostics {
		lines, st = cc.Filter(lines, true)
		changed = st.Dropped > 0 || st.Deduped > 0
	}
	var out []string
	run := 0
	flush := func() {
		if run > 0 {
			out = append(out, fmt.Sprintf("…+%d build steps", run))
			changed = true
		}
		run = 0
	}
	for i, l := range lines {
		if m := stepRE.FindStringSubmatch(l); m != nil {
			if m[1] == m[2] { // [M/M]: the outcome of the build, keep it
				flush()
				out = append(out, l)
				continue
			}
			run++
			continue
		}
		flush()
		if diagnostics && i > 0 && strings.HasPrefix(lines[i-1], "FAILED:") && len(l) > maxCommand {
			l = fmt.Sprintf("%s …+%d chars", l[:maxCommand], len(l)-maxCommand)
			changed = true
		}
		out = append(out, l)
	}
	flush()
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if diagnostics {
		out = append(out, cc.Footer(st, true)...)
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}
