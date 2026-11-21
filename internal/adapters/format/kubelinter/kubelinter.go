// Package kubelinter implements a format.Formatter for `kube-linter lint`.
// Findings are grouped per file with the failing object, check name and
// message; the per-finding remediation hints (identical for a given check)
// and the version banner are counted and dropped.
package kubelinter

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders kube-linter output.
type Formatter struct{}

// New constructs a kube-linter Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every kube-linter invocation; only `lint` is rendered.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "kube-linter"} }

var (
	findRE   = regexp.MustCompile(`^(.+?): \(object: (.*?)\) (.*) \(check: ([A-Za-z0-9_-]+), remediation: (.*)\)$`)
	objRE    = regexp.MustCompile(`^(.*?)/(\S+) \S+, Kind=(\w+)$`)
	bannerRE = regexp.MustCompile(`^KubeLinter \S+$`)
)

// Aggressive parses the lint report. kube-linter exits 1 when it finds lint
// errors, so recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if len(in.Argv) < 2 || in.Argv[1] != "lint" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if v, ok := lintkit.OptionValue(in.Argv, "--format"); ok && v != "plain" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	var fs []lintkit.Finding
	banner := 0
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if strings.TrimSpace(l) == "" {
			continue
		}
		if bannerRE.MatchString(l) {
			banner++
			continue
		}
		m := findRE.FindStringSubmatch(l)
		if m == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		obj := m[2]
		if o := objRE.FindStringSubmatch(m[2]); o != nil {
			obj = o[3] + "/" + o[2]
			if o[1] != "<no namespace>" {
				obj = o[3] + "/" + o[1] + "/" + o[2]
			}
		}
		fs = append(fs, lintkit.Finding{File: m[1], Code: m[4], Msg: obj + ": " + m[3]})
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := lintkit.Render("kube-linter", fs, lintkit.Dropped{Label: "remediation hints", N: len(fs)},
		lintkit.Dropped{Label: "banner line", N: banner})
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
