// Package cmake formats `cmake` configure and `cmake --build` output.
// Configure-time probe lines ("-- Detecting C compiler ABI info", "-- Looking
// for ...") and make-generator progress lines ("[ 42%] Building C object ...")
// collapse into counted markers; compiler diagnostics inside the stream are
// filtered by the cc package; CMake Error/Warning blocks and every other line
// pass through verbatim.
package cmake

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/cc"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	probeRE = regexp.MustCompile(`^-- (Detecting |Check for working |Check size of |Check if |Looking for |Performing Test |Checking )`)
	stepRE  = regexp.MustCompile(`^\[\s*\d+%\] (Building |Linking |Generating |Scanning |Copying |Consolidate |Running |Built target |Automatic |Swift |Creating |Compiling )`)
)

// Formatter renders cmake output.
type Formatter struct{}

// New constructs a cmake Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every cmake invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "cmake"} }

// Aggressive collapses probes and progress and filters compiler diagnostics.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed collapses probes and progress only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, diagnostics bool) (format.Rendered, error) {
	if !textOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
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
	lines, probes, steps := collapse(lines)
	if probes+steps > 0 {
		changed = true
	}
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if diagnostics {
		lines = append(lines, cc.Footer(st, true)...)
	}
	body := strings.Join(lines, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

// collapse replaces each run of probe lines, and each run of build-step lines,
// with one counted marker at the run's position. A stream that ENDS on a
// "Built target" line keeps it: it is the "[100%] Built target app" outcome.
func collapse(lines []string) (out []string, probes, steps int) {
	lastStep := -1
	for i, l := range lines {
		if stepRE.MatchString(l) && strings.Contains(l, "Built target ") {
			lastStep = i
		} else if stepRE.MatchString(l) {
			lastStep = -1
		}
	}
	runKind, runN := "", 0
	flush := func() {
		if runN > 0 {
			out = append(out, fmt.Sprintf("…+%d %s", runN, runKind))
		}
		runKind, runN = "", 0
	}
	for i, l := range lines {
		kind := ""
		switch {
		case probeRE.MatchString(l):
			kind = "configure probe lines"
		case stepRE.MatchString(l) && i != lastStep:
			kind = "build progress lines"
		}
		if kind == "" {
			flush()
			out = append(out, l)
			continue
		}
		if kind != runKind {
			flush()
			runKind = kind
		}
		runN++
		if kind == "configure probe lines" {
			probes++
		} else {
			steps++
		}
	}
	flush()
	return out, probes, steps
}

// textOutput declines for the modes whose output IS the answer: tool mode,
// script mode, version/help, variable listing, installs, traces.
func textOutput(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "-E", "-P", "--version", "-version", "--help", "-h", "-help", "--install", "--find-package",
			"-L", "-LA", "-LH", "-LAH", "--system-information", "--trace", "--trace-expand", "--debug-output",
			"--debug-find", "--graphviz", "--list-presets":
			return false
		}
		if strings.HasPrefix(a, "--help") || strings.HasPrefix(a, "--trace") || strings.HasPrefix(a, "--graphviz") {
			return false
		}
	}
	return true
}
