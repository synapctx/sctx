// Package grype implements a format.Formatter for grype's default table.
// Every Critical and High finding is kept per package with its fixed version
// (and a KEV marker); Medium/Low/Negligible/Unknown findings are reduced to
// per-package counts, and the EPSS/RISK columns are dropped.
package grype

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/adapters/format/scankit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders grype output.
type Formatter struct{}

// New constructs a grype Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every grype invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "grype"} }

var colRE = regexp.MustCompile(`\S+(?: \S+)*`)

type column struct {
	name       string
	start, end int
}

func cell(l string, c column) string {
	r := []rune(l)
	if c.start >= len(r) {
		return ""
	}
	end := c.end
	if end < 0 || end > len(r) {
		end = len(r)
	}
	return strings.TrimSpace(string(r[c.start:end]))
}

// Aggressive parses the table. grype exits 0 on findings unless --fail-on
// trips, so recognition of the table decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if len(in.Argv) > 1 {
		switch in.Argv[1] {
		case "db", "version", "config", "completion", "help":
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	if v, ok := lintkit.OptionValue(in.Argv, "-o", "--output"); ok && v != "table" && v != "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if lintkit.HasFlag(in.Argv, "--file", "--version", "-h", "--help") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	lines := lintkit.Lines(raw)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "NAME") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// Column starts are rune offsets of the header words.
	var cols []column
	for _, loc := range colRE.FindAllStringIndex(lines[0], -1) {
		cols = append(cols, column{name: lines[0][loc[0]:loc[1]], start: len([]rune(lines[0][:loc[0]])), end: -1})
	}
	for i := 0; i+1 < len(cols); i++ {
		cols[i].end = cols[i+1].start
	}
	idx := map[string]column{}
	for _, c := range cols {
		idx[c.name] = c
	}
	for _, need := range []string{"NAME", "INSTALLED", "FIXED IN", "VULNERABILITY", "SEVERITY"} {
		if _, ok := idx[need]; !ok {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	var vs []scankit.Vuln
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		v := scankit.Vuln{
			Pkg:       cell(l, idx["NAME"]),
			Installed: cell(l, idx["INSTALLED"]),
			ID:        cell(l, idx["VULNERABILITY"]),
			Sev:       strings.ToUpper(cell(l, idx["SEVERITY"])),
			Fixed:     cell(l, idx["FIXED IN"]),
		}
		if v.Pkg == "" || v.ID == "" || v.Sev == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.HasPrefix(v.Fixed, "(") { // "(won't fix)"
			v.Note, v.Fixed = v.Fixed, ""
		}
		if r, ok := idx["RISK"]; ok && strings.Contains(cell(l, r), "(kev)") {
			v.Note = strings.TrimSpace(v.Note + " KEV")
		}
		vs = append(vs, v)
	}
	if len(vs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	counts := map[string]int{}
	for _, v := range vs {
		counts[v.Sev]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "grype: %d vulnerabilities (", len(vs))
	var parts []string
	for _, s := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "NEGLIGIBLE", "UNKNOWN"} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", strings.ToLower(s), counts[s]))
		}
	}
	b.WriteString(strings.Join(parts, " ") + ")")
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
