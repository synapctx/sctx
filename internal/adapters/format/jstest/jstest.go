// Package jstest implements format.Formatter for the jest and vitest test
// runners. A passing run collapses to one summary line; a failing run keeps
// the summary plus, per failing test, its assertion/diff message and the first
// stack frame in the user's code. Code frames and library frames are dropped
// behind explicit markers.
//
// Recognition is by output shape (jest's "Tests:" and vitest's "Tests  "
// summary blocks), never by exit code. The reporters' own `--json` output is
// handled when it arrives alone on stdout.
package jstest

import (
	"context"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

// msgCap bounds one failure's message lines; the rest sits behind a marker.
const msgCap = 60

// Formatter renders one runner's output.
type Formatter struct {
	kind string // "jest" or "vitest"
}

// NewJest constructs the jest formatter.
func NewJest() *Formatter { return &Formatter{kind: "jest"} }

// NewVitest constructs the vitest formatter.
func NewVitest() *Formatter { return &Formatter{kind: "vitest"} }

// All returns the jest and vitest formatters.
func All() []format.Formatter { return []format.Formatter{NewJest(), NewVitest()} }

// Descriptor claims every invocation of the runner's binary.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.kind} }

// Aggressive renders the summary-and-failures view.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return Render(f.kind, in)
}

// Relaxed collapses provably redundant runs only.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr := jsutil.Streams(in)
	outLines, c1 := collapse.Runs(collapse.SplitLines(stdout))
	errLines, c2 := collapse.Runs(collapse.SplitLines(stderr))
	if !c1 && !c2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append(append([]string(nil), outLines...), errLines...)
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0}, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// failure is one failing test (or a suite that failed to run).
type failure struct {
	file, title string
	msg         []string
	frame       string
	hiddenStack int
	codeFrames  int
}

// result is a parsed run, whichever reporter produced it.
type result struct {
	kind         string
	testsFailed  int
	counts       []string // "409 passed", "2 skipped" ...
	suitesTotal  int
	suitesFailed int
	duration     string
	failures     []failure
	dropped      int // unrecognised lines outside failure blocks
	hasSummary   bool
	coverage     []string // jest --coverage table, trimmed
}

// Render parses a captured run. kind is "jest", "vitest", or "" to detect the
// reporter from the output (used by the npx/npm delegation). Output that is
// not recognised, or whose failure count disagrees with its own summary,
// declines so the chain degrades rather than hiding signal.
func Render(kind string, in format.Input) (format.Rendered, error) {
	stdout, stderr := jsutil.Streams(in)
	var r result
	var ok bool
	if j, jok := parseJSON(stdout, stderr, kind, in); jok {
		r, ok = j, true
	}
	if !ok {
		lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))
		switch kind {
		case "jest":
			r, ok = parseJest(lines)
		case "vitest":
			r, ok = parseVitest(lines)
		default:
			if r, ok = parseJest(lines); !ok {
				r, ok = parseVitest(lines)
			}
		}
	}
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if (r.testsFailed > 0 || in.ExitCode != 0) && len(r.failures) == 0 {
		// A non-zero exit with nothing we can attribute (coverage threshold,
		// crash, unhandled errors): keep the raw output.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return r.render(len(stderr) > 0), nil
}

func (r *result) summaryLine() string {
	var parts []string
	parts = append(parts, r.counts...)
	unit := "suites"
	if r.kind == "vitest" {
		unit = "files"
	}
	if r.suitesTotal > 0 {
		s := itoa(r.suitesTotal) + " " + unit
		if r.suitesFailed > 0 {
			s += " (" + itoa(r.suitesFailed) + " failed)"
		}
		parts = append(parts, s)
	}
	if r.duration != "" {
		parts = append(parts, r.duration)
	}
	return r.kind + ": " + strings.Join(parts, ", ")
}

func (r *result) render(foldStderr bool) format.Rendered {
	var b strings.Builder
	b.WriteString(r.summaryLine())
	elided := r.dropped > 0
	codeFrames := 0
	for _, f := range r.failures {
		b.WriteString("\nFAIL " + f.title)
		if f.file != "" {
			b.WriteString(" — " + f.file)
		}
		msg := f.msg
		if len(msg) > msgCap {
			elided = true
			msg = msg[:msgCap]
		}
		for _, l := range msg {
			b.WriteString("\n  " + l)
		}
		if len(f.msg) > msgCap {
			b.WriteString("\n  …+" + itoa(len(f.msg)-msgCap) + " lines")
		}
		if f.frame != "" || f.hiddenStack > 0 {
			frame := f.frame
			if frame == "" {
				frame = "(no user frame)"
			}
			b.WriteString("\n  at " + frame)
			if f.hiddenStack > 0 {
				b.WriteString(" (…+" + itoa(f.hiddenStack) + " frames)")
				elided = true
			}
		}
		codeFrames += f.codeFrames
	}
	if codeFrames > 0 {
		b.WriteString("\n…+" + itoa(codeFrames) + " code-frame lines")
		elided = true
	}
	for _, l := range r.coverage {
		b.WriteString("\n" + l)
		if strings.HasPrefix(l, "…+") {
			elided = true
		}
	}
	if r.dropped > 0 {
		b.WriteString("\n…+" + itoa(r.dropped) + " other lines")
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: foldStderr, Elided: elided}
}
