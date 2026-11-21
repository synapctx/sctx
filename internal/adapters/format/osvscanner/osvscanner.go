// Package osvscanner implements a format.Formatter for osv-scanner's default
// vulnerability table. Every Critical and High finding is kept per package
// with its fixed version and aliases; Medium/Low/Unknown findings are reduced
// to per-package counts (severity bucketed from the CVSS score), and the
// scan summary lines are kept.
package osvscanner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/adapters/format/scankit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders osv-scanner output.
type Formatter struct{}

// New constructs an osv-scanner Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every osv-scanner invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "osv-scanner"} }

const urlPrefix = "https://osv.dev/"

func severity(cvss string) string {
	f, err := strconv.ParseFloat(cvss, 64)
	switch {
	case err != nil:
		return "UNKNOWN"
	case f >= 9:
		return "CRITICAL"
	case f >= 7:
		return "HIGH"
	case f >= 4:
		return "MEDIUM"
	}
	return "LOW"
}

func cells(l string) []string {
	parts := strings.Split(l, "|")
	if len(parts) < 3 {
		return nil
	}
	out := make([]string, 0, len(parts)-2)
	for _, p := range parts[1 : len(parts)-1] {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// Aggressive parses the table. osv-scanner exits 1 when vulnerabilities are
// found, so recognition of the table decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if v, ok := lintkit.OptionValue(in.Argv, "-f", "--format"); ok && v != "table" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if _, ok := lintkit.OptionValue(in.Argv, "--output", "--output-file"); ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)

	var pre []string
	var vs []scankit.Vuln
	var srcs []string
	seenSrc := map[string]bool{}
	sawHeader := false
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		switch {
		case strings.TrimSpace(l) == "", strings.HasPrefix(l, "+-"):
			continue
		case strings.HasPrefix(l, "|"):
			c := cells(l)
			if !sawHeader {
				if len(c) != 7 || c[0] != "OSV URL" || c[1] != "CVSS" || c[3] != "PACKAGE" || c[4] != "VERSION" || c[5] != "FIXED VERSION" {
					return format.Rendered{}, format.ErrTierInapplicable
				}
				sawHeader = true
				continue
			}
			if len(c) != 7 {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			if c[1] == "" && c[3] == "" && len(vs) > 0 { // alias row of the previous vulnerability
				if id := strings.TrimPrefix(c[0], urlPrefix); id != "" {
					v := &vs[len(vs)-1]
					v.Note = strings.TrimSpace(v.Note + " " + id)
				}
				continue
			}
			vs = append(vs, scankit.Vuln{Pkg: c[3], Installed: c[4], ID: strings.TrimPrefix(c[0], urlPrefix),
				Sev: severity(c[1]), Fixed: c[5]})
			if c[6] != "" && !seenSrc[c[6]] {
				seenSrc[c[6]] = true
				srcs = append(srcs, c[6])
			}
		default:
			pre = append(pre, strings.TrimSpace(l))
		}
	}
	if !sawHeader || len(vs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for i := range vs {
		if scankit.IsHigh(vs[i].Sev) && vs[i].Note != "" {
			vs[i].Note = "aka " + strings.ReplaceAll(vs[i].Note, " ", ",")
		} else {
			vs[i].Note = ""
		}
	}
	var b strings.Builder
	b.WriteString("osv-scanner:")
	for _, p := range pre {
		b.WriteString("\n" + p)
	}
	if len(srcs) > 1 {
		b.WriteString("\nsources: " + strings.Join(srcs, ", "))
	}
	scankit.RenderVulns(&b, vs)
	if t := strings.TrimSpace(lintkit.StripANSI(string(errRaw))); t != "" {
		b.WriteString("\n" + t)
	}
	body := b.String()
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true,
		Note: fmt.Sprintf("%d vulns", len(vs))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
