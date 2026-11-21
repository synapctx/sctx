// Package tfsec implements a format.Formatter for tfsec's default report
// (coloured even when piped). Each result becomes a severity/rule/file:line
// finding grouped per file; code excerpts, impact/resolution text, doc links,
// timing tables and the migration banner are counted and dropped, and the
// "N passed, M potential problem(s)" totals are kept.
package tfsec

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders tfsec output.
type Formatter struct{}

// New constructs a tfsec Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every tfsec invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "tfsec"} }

var (
	resultRE = regexp.MustCompile(`^Result #(\d+) (CRITICAL|HIGH|MEDIUM|LOW|INFO|UNKNOWN)\s+(.*?)\s*$`)
	locRE    = regexp.MustCompile(`^\s*(\S+?):(\d+)(?:-\d+)?\s*$`)
	idRE     = regexp.MustCompile(`^\s+ID\s+(\S+)\s*$`)
	regoRE   = regexp.MustCompile(`^\s+Rego Package\s+(\S+)\s*$`)
	totalRE  = regexp.MustCompile(`^\s*(\d+ passed.*potential problem\(s\) detected\.|No problems detected!)\s*$`)
	timingRE = regexp.MustCompile(`^\s*timings\s*$`)
)

// Aggressive parses the report. tfsec exits 1 when problems are found, so
// recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if v, ok := lintkit.OptionValue(in.Argv, "-f", "--format"); ok && v != "default" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if lintkit.HasFlag(in.Argv, "--concise-output", "--version", "--help", "-h") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	var fs []lintkit.Finding
	var cur *lintkit.Finding
	var totals []string
	dropped := 0
	trailer := false
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if m := resultRE.FindStringSubmatch(t); m != nil {
			fs = append(fs, lintkit.Finding{Sev: strings.ToLower(m[2]), Msg: m[3]})
			cur = &fs[len(fs)-1]
			trailer = false
			continue
		}
		if totalRE.MatchString(l) {
			totals = append(totals, t)
			cur, trailer = nil, true
			continue
		}
		if timingRE.MatchString(l) {
			cur, trailer = nil, true
			dropped++
			continue
		}
		if cur == nil {
			if trailer || len(fs) == 0 {
				dropped++ // timings/counts tables, migration banner, preamble
				continue
			}
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if m := idRE.FindStringSubmatch(l); m != nil {
			cur.Code = m[1]
			continue
		}
		if m := regoRE.FindStringSubmatch(l); m != nil {
			if cur.Code == "" {
				cur.Code = m[1]
			}
			continue
		}
		if cur.File == "" {
			if m := locRE.FindStringSubmatch(l); m != nil {
				cur.File = m[1]
				cur.Line, _ = strconv.Atoi(m[2])
				continue
			}
		}
		dropped++
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for _, f := range fs {
		if f.File == "" || f.Code == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	body := lintkit.Render("tfsec", fs, lintkit.Dropped{Label: "code/impact/resolution/link/banner lines", N: dropped})
	for _, t := range totals {
		body += "\n" + t
	}
	if t := strings.TrimSpace(lintkit.StripANSI(string(errRaw))); t != "" {
		body += "\n" + t
	}
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true,
		Note: fmt.Sprintf("%d findings", len(fs))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
