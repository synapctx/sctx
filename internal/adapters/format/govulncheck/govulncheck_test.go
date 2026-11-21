package govulncheck

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: govulncheck v1.10.0 against a module pinned to golang.org/x/text
// v0.3.0 (never executed; only analysed), macOS arm64, go1.27.4.

func inputOf(t *testing.T, name string, argv ...string) (format.Input, int) {
	t.Helper()
	read := func(k string) string {
		b, err := os.ReadFile("testdata/" + name + "." + k + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if len(argv) == 0 {
		argv = []string{"govulncheck", "./..."}
	}
	o, e := read("stdout"), read("stderr")
	return format.Input{Argv: argv, ExitCode: 3, Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestAggressiveKeepsEveryFinding(t *testing.T) {
	common := []string{
		"GO-2022-1059 golang.org/x/text@v0.3.0 -> v0.3.8 [called]",
		"Denial of service via crafted Accept-Language header in golang.org/x/text/language",
		"GO-2021-0113 golang.org/x/text@v0.3.0 -> v0.3.7 [called]",
		"Out-of-bounds read in golang.org/x/text/language",
		"Your code is affected by 2 vulnerabilities from 1 module.",
		"3\nvulnerabilities in modules you require",
	}
	cases := []struct {
		name string
		want []string
	}{
		{"vuln", append([]string{"main.go:10:44: vuln.main calls language.ParseAcceptLanguage", "more-info URLs ×2", "verbose hint ×1"}, common...)},
		{"vuln-traces", append([]string{"main @ example.com/vuln/main.go:10:44", "ParseAcceptLanguage @ golang.org/x/text/language/parse.go:775:6", "#1: for function golang.org/x/text/language.ParseAcceptLanguage"}, common...)},
		{"vuln-bin", append([]string{"#1: language.Parse", "#2: language.ParseAcceptLanguage", "2 trace(s)"}, common...)},
		{"vuln-show", append([]string{
			"GO-2026-6629 golang.org/x/text@v0.3.0 -> v0.41.0 [required]",
			"GO-2026-5970 golang.org/x/text@v0.3.0 -> v0.39.0 [required]",
			"GO-2020-0015 golang.org/x/text@v0.3.0 -> v0.3.3 [required]",
			"No other vulnerabilities found.", "scanned the following 2 modules and the go1.27.4 standard library",
		}, common...)},
		{"vuln-bin-verbose", append([]string{"GO-2026-6629", "GO-2026-5970", "GO-2020-0015", "[required]"}, common...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, raw := inputOf(t, tc.name)
			r, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, bad := range []string{"More info:", "Fetching vulnerabilities", "Use '-show verbose'", "Example traces found"} {
				if strings.Contains(body, bad) {
					t.Errorf("kept prose %q", bad)
				}
			}
			if len(body) >= raw {
				t.Errorf("not smaller: %d >= %d", len(body), raw)
			}
			if !r.Elided || !strings.Contains(body, "…+") {
				t.Error("elision not marked")
			}
		})
	}
}

func TestTraceCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("=== Symbol Results ===\n\nVulnerability #1: GO-1\n    Boom\n  More info: https://pkg.go.dev/vuln/GO-1\n  Module: m\n    Found in: m@v1\n    Fixed in: m@v2\n    Example traces found:\n")
	for i := 1; i <= 7; i++ {
		sb.WriteString("      #" + string(rune('0'+i)) + ": a.go:1:1: x calls y\n")
	}
	sb.WriteString("\nYour code is affected by 1 vulnerability from 1 module.\nUse '-show verbose' for more details.\n")
	in := format.Input{Argv: []string{"govulncheck", "./..."}, Stdout: strings.NewReader(sb.String()), Stderr: strings.NewReader("")}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if strings.Count(body, "calls y") != maxTraces || !strings.Contains(body, "7 trace(s)") || !strings.Contains(body, "…+4 more traces") {
		t.Fatalf("trace cap wrong:\n%s", body)
	}
}

func TestNoFixIsStated(t *testing.T) {
	src := "=== Symbol Results ===\n\nVulnerability #1: GO-9\n    T\n  More info: u\n  Module: m\n    Found in: m@v1\n    Fixed in: N/A\n\nYour code is affected by 1 vulnerability from 1 module.\n"
	in := format.Input{Argv: []string{"govulncheck", "./..."}, Stdout: strings.NewReader(src), Stderr: strings.NewReader("")}
	r, err := New().Aggressive(context.Background(), in)
	if err != nil || !strings.Contains(string(r.Body), "GO-9 m@v1 (no fix) [called]") {
		t.Fatalf("err=%v body=%s", err, r.Body)
	}
}

func TestDeclines(t *testing.T) {
	in, _ := inputOf(t, "vuln")
	for _, argv := range [][]string{
		{"govulncheck", "-json", "./..."}, {"govulncheck", "-format", "json", "./..."}, {"govulncheck", "--format=sarif", "./..."},
		{"govulncheck", "-format=openvex", "./..."}, {"govulncheck", "-version"},
	} {
		in.Argv = argv
		for _, tier := range []func(context.Context, format.Input) (format.Rendered, error){New().Aggressive, New().Relaxed} {
			if _, err := tier(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("%v: err=%v", argv, err)
			}
		}
	}
	// -format text is the default and stays handled.
	in2, _ := inputOf(t, "vuln", "govulncheck", "-format", "text", "./...")
	if _, err := New().Aggressive(context.Background(), in2); err != nil {
		t.Errorf("-format text must be handled: %v", err)
	}
	// A load failure is an error message, not a report: nothing to compact.
	in3, _ := inputOf(t, "vuln-ok")
	if _, err := New().Aggressive(context.Background(), in3); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("load failure must decline: %v", err)
	}
}

func TestRelaxedDropsOnlyBanner(t *testing.T) {
	in, _ := inputOf(t, "vuln-show")
	r, err := New().Relaxed(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, w := range []string{"More info: https://pkg.go.dev/vuln/GO-2022-1059", "Fixed in: golang.org/x/text@v0.3.8", "#1: main.go:10:44", "Vulnerability #3: GO-2020-0015"} {
		if !strings.Contains(body, w) {
			t.Errorf("relaxed lost %q", w)
		}
	}
	if strings.Contains(body, "Fetching vulnerabilities") || strings.Contains(body, "Checking the code") {
		t.Error("banner kept")
	}
}
