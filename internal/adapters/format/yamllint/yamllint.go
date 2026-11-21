// Package yamllint implements a format.Formatter for yamllint's standard
// report: a file heading followed by indented `line:col  severity  message
// (rule)` rows. Rows are regrouped per file with a rule histogram and the
// column padding removed.
package yamllint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

var rowRE = regexp.MustCompile(`^ +(\d+):(\d+) +(error|warning) +(.*?)(?:  \(([a-z0-9-]+)\))?$`)

// Formatter renders yamllint output.
type Formatter struct{}

// New constructs a yamllint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every yamllint invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "yamllint"} }

// userFormat reports -f parsable/github (or any non-human format): those
// are caller-chosen contracts.
func userFormat(argv []string) bool {
	v, ok := lintkit.OptionValue(argv, "-f", "--format")
	if !ok {
		return false
	}
	switch v {
	case "standard", "colored", "auto":
		return false
	}
	return true
}

// Aggressive regroups the standard report. yamllint exits 1 on errors (2 on
// strict warnings), so the report's shape decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if userFormat(in.Argv) || len(lintkit.ReadAll(in.Stderr)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	var fs []lintkit.Finding
	file := ""
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if l == "" {
			continue
		}
		if m := rowRE.FindStringSubmatch(l); m != nil && file != "" {
			ln, _ := strconv.Atoi(m[1])
			col, _ := strconv.Atoi(m[2])
			fs = append(fs, lintkit.Finding{File: file, Line: ln, Col: col, Sev: m[3], Code: m[5], Msg: m[4]})
			continue
		}
		if l[0] != ' ' {
			file = l
			continue
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := lintkit.Render("yamllint", fs)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d findings", len(fs))}, nil
}

// Relaxed declines: the aggressive tier already models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
