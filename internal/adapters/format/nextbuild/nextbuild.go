// Package nextbuild implements format.Formatter for `next build`. The build's
// phase banners, per-worker "Generating static pages (k/N)" progress, the
// telemetry notice and the glyph legend are noise; the Next.js version line,
// compile/type-check/build results, the route table, warnings and every error
// (with its file:line:col and message) are kept. The code frame under a build
// error is counted behind a marker.
package nextbuild

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	versionRE  = regexp.MustCompile(`^▲ Next\.js `)
	phaseRE    = regexp.MustCompile(`^(Creating an optimized production build \.\.\.|Running TypeScript \.\.\.|Finished TypeScript in .*|Collecting page data.*|Finalizing page optimization.*|Collecting build traces.*|Linting and checking validity of types.*|✓ Running next\.config.* took .*|Generating static pages using \d+ workers? \(\d+/\d+\)\s*(\.\.\.)?)$`)
	legendRE   = regexp.MustCompile(`^(○|●|ƒ|λ|◐|✓)\s+\((?:Static|SSG|Dynamic|Partial Prerender|ISR|Server)\)`)
	frameRE    = regexp.MustCompile(`^\s*>?\s*\d+\s*\|`)
	caretRE    = regexp.MustCompile(`^\s+\|\s*\^`)
	atRE       = regexp.MustCompile(`^\s+at (<unknown>|ignore-listed frames)`)
	telemetryR = regexp.MustCompile(`^(Attention: Next\.js now collects|This information is used to shape|You can learn more, including how to opt-out|https://nextjs\.org/telemetry$)`)
	warnRootRE = regexp.MustCompile(`^⚠ Warning: Next\.js inferred your workspace root`)
)

// Formatter renders `next build` output.
type Formatter struct{}

// New constructs the next build formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims `next build` only: `next dev` and `next start` are servers.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "next", Subcommands: []string{"build"}}
}

// Aggressive drops progress and frames.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return Render(in)
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

// Render is the aggressive tier, exported for npm-script delegation.
func Render(in format.Input) (format.Rendered, error) {
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))
	var (
		out     []string
		hidden  int
		sawNext bool
		inWarn  bool
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case versionRE.MatchString(t):
			sawNext = true
			out = append(out, t)
			continue
		case t == "":
			inWarn = false
			continue
		case warnRootRE.MatchString(t):
			inWarn = true
			out = append(out, t)
			continue
		case inWarn:
			hidden++
			continue
		case phaseRE.MatchString(t) && !strings.HasPrefix(t, "✓ Generating"), telemetryR.MatchString(t), legendRE.MatchString(t):
			continue
		case frameRE.MatchString(l), caretRE.MatchString(l), atRE.MatchString(l):
			hidden++
			continue
		}
		out = append(out, l)
	}
	if !sawNext {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept := out
	body := strings.Join(kept, "\n")
	if hidden > 0 {
		body += "\n…+" + strconv.Itoa(hidden) + " code-frame/detail lines"
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(stderr) > 0, Elided: hidden > 0}, nil
}
