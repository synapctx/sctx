// Package hadolint implements a format.Formatter for hadolint's default tty
// report (`file:line CODE severity: message`). hadolint colours its output
// even when piped; the colour is stripped, findings are grouped per file with
// a rule histogram, and repeated rule messages are shown once.
package hadolint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

var findingRE = regexp.MustCompile(`^(.+?):(\d+) ([A-Z]+\d+) (error|warning|info|style): (.*)$`)

// Formatter renders hadolint output.
type Formatter struct{}

// New constructs a hadolint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every hadolint invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "hadolint"} }

// machineFormat reports any -f/--format other than the human tty report.
func machineFormat(argv []string) bool {
	v, ok := lintkit.OptionValue(argv, "-f", "--format")
	return ok && v != "tty"
}

// Aggressive parses the report. hadolint exits 1 on findings above the
// failure threshold, so recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineFormat(in.Argv) || len(lintkit.ReadAll(in.Stderr)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	var fs []lintkit.Finding
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if l == "" {
			continue
		}
		m := findingRE.FindStringSubmatch(l)
		if m == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		ln, _ := strconv.Atoi(m[2])
		fs = append(fs, lintkit.Finding{File: m[1], Line: ln, Code: m[3], Sev: m[4], Msg: m[5]})
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := lintkit.Render("hadolint", fs)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d findings", len(fs))}, nil
}

// Relaxed declines: the aggressive tier already models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
