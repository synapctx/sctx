// Package markdownlint implements a format.Formatter for markdownlint-cli's
// issue report. markdownlint-cli writes its findings to STDERR, so the
// formatter reads stderr (and absorbs it, FoldStderr) and regroups
// `file:line[:col] severity MDxxx/alias description [detail] [context]` rows
// per file with a rule histogram.
package markdownlint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

var rowRE = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))? (error|warning) (MD\d+)/(\S+) (.*)$`)

// Formatter renders markdownlint output.
type Formatter struct{}

// New constructs a markdownlint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims markdownlint (the markdownlint-cli binary name).
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "markdownlint"} }

// userOutput reports modes whose bytes are a data contract or are redirected:
// --json, --output FILE, --fix (rewrites files), --quiet, --help/--version.
func userOutput(argv []string) bool {
	if lintkit.HasFlag(argv, "-j", "--json", "-f", "--fix", "-q", "--quiet", "-h", "--help", "-V", "--version", "-s", "--stdin") {
		return true
	}
	_, ok := lintkit.OptionValue(argv, "-o", "--output")
	return ok
}

// Aggressive regroups the report. markdownlint exits 1 on findings, so the
// report's shape decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if userOutput(in.Argv) || len(lintkit.ReadAll(in.Stdout)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stderr)
	var fs []lintkit.Finding
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if l == "" {
			continue
		}
		m := rowRE.FindStringSubmatch(l)
		if m == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		ln, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		fs = append(fs, lintkit.Finding{File: m[1], Line: ln, Col: col, Sev: m[4], Code: m[5], Msg: m[7]})
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := lintkit.Render("markdownlint", fs)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d findings", len(fs)), FoldStderr: true}, nil
}

// Relaxed declines: the aggressive tier already models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
