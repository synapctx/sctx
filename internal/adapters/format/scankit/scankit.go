// Package scankit holds the vulnerability-listing rendering shared by the
// dependency/image scanner formatters (grype, osv-scanner): every CRITICAL and
// HIGH finding is kept per package, lower severities are reduced to counts.
package scankit

import (
	"fmt"
	"strings"
)

// Vuln is one vulnerability finding against one package.
type Vuln struct {
	Pkg       string
	Installed string
	ID        string
	Sev       string // upper-case: CRITICAL HIGH MEDIUM LOW NEGLIGIBLE UNKNOWN
	Fixed     string // "" when no fix is known
	Note      string // short annotation, e.g. "KEV" or "won't fix"
}

// IsHigh reports whether the severity is always listed.
func IsHigh(sev string) bool { return sev == "CRITICAL" || sev == "HIGH" }

// RenderVulns writes the grouped listing: a line per package with its
// CRITICAL/HIGH findings underneath, then one marked line counting the rest.
func RenderVulns(b *strings.Builder, vs []Vuln) {
	type pk struct{ pkg, inst string }
	var order, restOrder []pk
	by := map[pk][]Vuln{}
	rest := map[pk]map[string]int{}
	for _, v := range vs {
		k := pk{v.Pkg, v.Installed}
		if IsHigh(v.Sev) {
			if _, ok := by[k]; !ok {
				order = append(order, k)
			}
			by[k] = append(by[k], v)
			continue
		}
		if rest[k] == nil {
			rest[k] = map[string]int{}
			restOrder = append(restOrder, k)
		}
		rest[k][v.Sev]++
	}
	for _, k := range order {
		b.WriteString("\n  " + strings.TrimSpace(k.pkg+" "+k.inst))
		for _, v := range by[k] {
			b.WriteString("\n    " + v.ID + " " + v.Sev)
			switch {
			case v.Fixed != "":
				b.WriteString(" -> " + v.Fixed)
			case !strings.HasPrefix(v.Note, "("):
				b.WriteString(" (no fix)")
			}
			if v.Note != "" {
				b.WriteString(" " + v.Note)
			}
		}
	}
	if len(restOrder) == 0 {
		return
	}
	total := 0
	var parts []string
	for _, k := range restOrder {
		var sevs []string
		for _, s := range []string{"MEDIUM", "LOW", "NEGLIGIBLE", "UNKNOWN"} {
			if n := rest[k][s]; n > 0 {
				sevs = append(sevs, fmt.Sprintf("%s×%d", strings.ToLower(s[:1]), n))
				total += n
			}
		}
		parts = append(parts, strings.TrimSpace(k.pkg+" "+k.inst)+" "+strings.Join(sevs, ","))
	}
	fmt.Fprintf(b, "\n  …+%d MEDIUM/LOW/NEGLIGIBLE/UNKNOWN (m=medium l=low n=negligible u=unknown): %s", total, strings.Join(parts, "; "))
}
