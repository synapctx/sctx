package scankit

import (
	"strings"
	"testing"
)

func TestRenderVulns(t *testing.T) {
	vs := []Vuln{
		{Pkg: "libssl3", Installed: "3.0.9", ID: "CVE-1", Sev: "CRITICAL", Fixed: "3.0.18"},
		{Pkg: "libssl3", Installed: "3.0.9", ID: "CVE-2", Sev: "HIGH", Note: "(won't fix)"},
		{Pkg: "libssl3", Installed: "3.0.9", ID: "CVE-3", Sev: "HIGH"},
		{Pkg: "libssl3", Installed: "3.0.9", ID: "CVE-4", Sev: "MEDIUM"},
		{Pkg: "zlib", Installed: "1.2", ID: "CVE-5", Sev: "LOW"},
		{Pkg: "zlib", Installed: "1.2", ID: "CVE-6", Sev: "NEGLIGIBLE"},
	}
	var b strings.Builder
	RenderVulns(&b, vs)
	got := b.String()
	for _, w := range []string{
		"  libssl3 3.0.9\n    CVE-1 CRITICAL -> 3.0.18\n    CVE-2 HIGH (won't fix)\n    CVE-3 HIGH (no fix)",
		"…+3 MEDIUM/LOW/NEGLIGIBLE/UNKNOWN", "libssl3 3.0.9 m×1", "zlib 1.2 l×1,n×1",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func TestOnlyHighHasNoRestLine(t *testing.T) {
	var b strings.Builder
	RenderVulns(&b, []Vuln{{Pkg: "a", ID: "X", Sev: "HIGH"}})
	if strings.Contains(b.String(), "…+") {
		t.Fatalf("unexpected elision marker: %s", b.String())
	}
}
