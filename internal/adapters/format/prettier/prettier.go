// Package prettier implements format.Formatter for `prettier --check` and
// `prettier --write`. A clean check collapses to one line; a failing check
// keeps the list of files that need formatting plus every syntax error's
// message (its code frame is counted behind a marker); a write run drops the
// per-file timings and lists only the files that actually changed.
//
// `--list-different` / `-l` already print bare paths and decline.
package prettier

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
	warnFileRE = regexp.MustCompile(`^\[warn\] (\S.*)$`)
	issuesRE   = regexp.MustCompile(`^\[warn\] Code style issues found in (?:the above file|\d+ files?)\b`)
	errLineRE  = regexp.MustCompile(`^\[error\] (.*)$`)
	frameRE    = regexp.MustCompile(`^\[error\]\s+(?:>\s*)?(?:\d+ )?\|`)
	frameNumRE = regexp.MustCompile(`^\[error\]\s+>?\s*\d+ \|`)
	writeRE    = regexp.MustCompile(`^(\S.*?) (\d+)ms((?: \((?:unchanged|cached)\))*)$`)
)

// Formatter renders prettier output.
type Formatter struct{}

// New constructs the prettier formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every prettier invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "prettier"} }

// Aggressive renders the check/write summary.
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

// Render is the aggressive tier, exported for the npx / package-manager
// delegation.
func Render(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv {
		if a == "--list-different" || a == "-l" || a == "--help" || a == "-h" || a == "--version" || a == "-v" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))

	var (
		needFmt   []string
		changed   []string
		unchanged int
		errs      []string
		frames    int
		other     []string
		checking  bool
		clean     bool
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
		case t == "Checking formatting...":
			checking = true
		case strings.HasPrefix(t, "All matched files use Prettier code style"):
			clean = true
		case issuesRE.MatchString(l):
		case frameNumRE.MatchString(l) || frameRE.MatchString(l):
			frames++
		case errLineRE.MatchString(l):
			errs = append(errs, errLineRE.FindStringSubmatch(l)[1])
		case warnFileRE.MatchString(l):
			needFmt = append(needFmt, warnFileRE.FindStringSubmatch(l)[1])
		case writeRE.MatchString(l):
			m := writeRE.FindStringSubmatch(l)
			if strings.Contains(m[3], "unchanged") {
				unchanged++
			} else {
				changed = append(changed, m[1])
			}
		default:
			other = append(other, t)
		}
	}
	wrote := len(changed)+unchanged > 0
	if !checking && !wrote {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	switch {
	case checking && clean && len(needFmt) == 0 && len(errs) == 0:
		b.WriteString("prettier: all matched files formatted")
	case checking:
		b.WriteString("prettier: " + strconv.Itoa(len(needFmt)) + " " + jsutil.Plural(len(needFmt), "file needs", "files need") + " formatting")
		if len(errs) > 0 {
			b.WriteString(", errors")
		}
		for _, f := range needFmt {
			b.WriteString("\n" + f)
		}
	default:
		b.WriteString("prettier: " + strconv.Itoa(len(changed)) + " written, " + strconv.Itoa(unchanged) + " unchanged")
		for _, f := range changed {
			b.WriteString("\n" + f)
		}
	}
	for _, e := range errs {
		b.WriteString("\nerror: " + e)
	}
	for _, o := range other {
		b.WriteString("\n" + o)
	}
	elided := frames > 0
	if frames > 0 {
		b.WriteString("\n…+" + strconv.Itoa(frames) + " code-frame lines")
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(stderr) > 0, Elided: elided}, nil
}
