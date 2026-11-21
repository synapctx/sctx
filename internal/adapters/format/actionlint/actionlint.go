// Package actionlint implements a format.Formatter for actionlint's default
// error output. Each finding keeps file:line:col, rule kind and message;
// the three-line source excerpt under it is dropped and counted, and
// pathologically long messages (runner label lists, context type dumps) are
// cut with a visible character count.
package actionlint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	findingRE = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.*)$`)
	kindRE    = regexp.MustCompile(`^(.*) \[([a-z0-9-]+)\]$`)
	sourceRE  = regexp.MustCompile(`^ *\d+ \|`)
	excerptRE = regexp.MustCompile(`^ *\d* \|(?: .*)?$`)
)

// Formatter renders actionlint output.
type Formatter struct{}

// New constructs an actionlint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every actionlint invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "actionlint"} }

// userFormat reports -format / -oneline, which replace the human report with
// a caller-chosen contract.
func userFormat(argv []string) bool {
	if _, ok := lintkit.OptionValue(argv, "-format", "--format"); ok {
		return true
	}
	return lintkit.HasFlag(argv, "-oneline", "--oneline", "-version", "--version", "-h", "-help", "--help")
}

// Aggressive condenses the report. actionlint exits 1 on findings, so the
// report's shape, not the exit code, decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if userFormat(in.Argv) || len(lintkit.ReadAll(in.Stderr)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	var fs []lintkit.Finding
	excerpts := 0
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if m := findingRE.FindStringSubmatch(l); m != nil && !excerptRE.MatchString(l) {
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			f := lintkit.Finding{File: m[1], Line: ln, Col: col, Msg: m[4]}
			if k := kindRE.FindStringSubmatch(m[4]); k != nil {
				f.Msg, f.Code = k[1], "["+k[2]+"]"
			}
			fs = append(fs, f)
			continue
		}
		if excerptRE.MatchString(l) && len(fs) > 0 {
			if sourceRE.MatchString(l) {
				excerpts++
			}
			continue
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := lintkit.Render("actionlint", fs, lintkit.Dropped{Label: "source excerpts", N: excerpts})
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d findings", len(fs))}, nil
}

// Relaxed declines: the report has no safe line-level noise beyond what the
// aggressive tier already models.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
