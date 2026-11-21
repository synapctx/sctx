// Package tflint implements a format.Formatter for tflint's default text
// report. Findings are grouped per file with severity, rule and message;
// the per-finding "Reference:" documentation links and source-line echoes are
// counted and dropped.
package tflint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders tflint output.
type Formatter struct{}

// New constructs a tflint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every tflint invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "tflint"} }

var (
	headRE   = regexp.MustCompile(`^(\d+) issue\(s\) found:$`)
	issueRE  = regexp.MustCompile(`^(Error|Warning|Notice): (.*) \(([A-Za-z0-9_-]+)\)$`)
	onRE     = regexp.MustCompile(`^\s+on (.+) line (\d+):$`)
	srcRE    = regexp.MustCompile(`^\s+\d+: `)
	refRE    = regexp.MustCompile(`^Reference: `)
	srcNoRE  = regexp.MustCompile(`^\s+\(source code not available\)$`)
	sevShort = map[string]string{"Error": "error", "Warning": "warning", "Notice": "notice"}
)

// Aggressive parses the report. tflint exits 2 when issues are found, so
// recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if v, ok := lintkit.OptionValue(in.Argv, "-f", "--format"); ok && v != "default" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if lintkit.HasFlag(in.Argv, "--version", "-v", "--init", "--help", "-h") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	var fs []lintkit.Finding
	want := -1
	dropped := 0
	var cur *lintkit.Finding
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := headRE.FindStringSubmatch(l); m != nil {
			want, _ = strconv.Atoi(m[1])
			continue
		}
		if m := issueRE.FindStringSubmatch(l); m != nil {
			fs = append(fs, lintkit.Finding{Sev: sevShort[m[1]], Msg: m[2], Code: m[3]})
			cur = &fs[len(fs)-1]
			continue
		}
		if cur == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if m := onRE.FindStringSubmatch(l); m != nil {
			cur.File = m[1]
			cur.Line, _ = strconv.Atoi(m[2])
			continue
		}
		if srcRE.MatchString(l) || refRE.MatchString(l) || srcNoRE.MatchString(l) {
			dropped++
			continue
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(fs) == 0 || (want >= 0 && want != len(fs)) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for _, f := range fs {
		if f.File == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	body := lintkit.Render("tflint", fs, lintkit.Dropped{Label: "reference links/source echoes", N: dropped})
	if t := strings.TrimSpace(string(errRaw)); t != "" {
		body += "\n" + lintkit.StripANSI(t)
	}
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: dropped > 0,
		Note: fmt.Sprintf("%d findings", len(fs))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
