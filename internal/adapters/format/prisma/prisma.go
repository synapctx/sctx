// Package prisma implements format.Formatter for the Prisma CLI (generate,
// migrate, db push, validate, ...). The CLI's banner lines, the multi-line
// "update available" box and the code frame under a schema error are noise;
// the result line, applied-migration list and every error message with its
// error code and schema location are kept.
//
// `studio` and `dev` run servers and `--help` is documentation, so those
// decline (and the first two must never be wrapped at all).
package prisma

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
	updateRE  = regexp.MustCompile(`Update available (\S+) -> (\S+)`)
	frameRE   = regexp.MustCompile(`^\s*\d*\s*\|`)
	boxTopRE  = regexp.MustCompile(`^┌─+┐$`)
	boxBotRE  = regexp.MustCompile(`^└─+┘$`)
	bannerRE  = regexp.MustCompile(`^(Loaded Prisma config from |Prisma schema loaded from |Environment variables loaded from )`)
	versionRE = regexp.MustCompile(`^(Prisma CLI Version\s*:|\[Context: )`)
	skillsRE  = regexp.MustCompile(`^Prisma agent skills are out of date`)
)

// Formatter renders prisma output.
type Formatter struct{}

// New constructs the prisma formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every prisma invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "prisma"} }

// Aggressive drops banners, the update box and code frames.
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

// Render is the aggressive tier, exported for npx / package-manager delegation.
func Render(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv[min(1, len(in.Argv)):] {
		switch a {
		case "--help", "-h", "--version", "-v", "studio", "dev", "--watch":
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))

	var (
		out    []string
		frames int
		other  int
		known  bool
		box    []string
		inBox  bool
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if inBox {
			box = append(box, l)
			if boxBotRE.MatchString(t) {
				inBox = false
				if m := updateRE.FindStringSubmatch(strings.Join(box, "\n")); m != nil {
					out = append(out, "update available: "+m[1]+" -> "+m[2])
				} else {
					out = append(out, box...)
				}
			}
			continue
		}
		switch {
		case boxTopRE.MatchString(t):
			inBox, box = true, []string{l}
		case bannerRE.MatchString(t):
			known = true
		case versionRE.MatchString(t), skillsRE.MatchString(t):
			known = true
			other++
		case strings.HasPrefix(t, "Error:") || strings.HasPrefix(t, "error:") || strings.Contains(t, "Error code:"):
			known = true
			out = append(out, l)
		case frameRE.MatchString(t) && strings.Contains(t, "|"):
			frames++
		case t == "":
			// Blank lines only separated the banners; none carry content.
		default:
			out = append(out, l)
		}
	}
	if inBox {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if !known {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	b.WriteString(strings.Join(out, "\n"))
	elided := frames > 0 || other > 0
	if n := frames + other; n > 0 {
		b.WriteString("\n…+" + strconv.Itoa(n) + " code-frame/footer lines")
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(stderr) > 0, Elided: elided}, nil
}
