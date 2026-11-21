package osvscanner

import (
	"context"
	"errors"
	"flag"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Fixture: osv-scanner v2.2.2 (docker, linux) `scan source -L` over a
// requirements.txt pinning old django/requests/pyyaml/urllib3.

func load(t *testing.T, argv ...string) (format.Input, int) {
	t.Helper()
	rd := func(ext string) string {
		b, err := os.ReadFile("testdata/scan." + ext)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(rd("exit")))
	o, e := rd("stdout"), rd("stderr")
	return format.Input{Argv: append([]string{"osv-scanner"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	in, rawLen := load(t, "scan", "source", "-L", "py/requirements.txt")
	got, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("testdata/scan.golden", got.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := os.ReadFile("testdata/scan.golden")
	if string(got.Body) != string(want) {
		t.Fatalf("golden mismatch:\n%s", got.Body)
	}
	if len(got.Body) >= rawLen {
		t.Fatalf("not smaller: %d >= %d", len(got.Body), rawLen)
	}
	if !got.Elided || !got.FoldStderr {
		t.Fatal("Elided/FoldStderr must be set")
	}
	for _, w := range []string{"Total 5 packages affected by 65 known vulnerabilities (9 Critical, 26 High", "65 vulnerabilities can be fixed.", "…+"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q", w)
		}
	}
}

// Every vulnerability with CVSS >= 7 must be listed under its package.
func TestEveryCriticalAndHighKept(t *testing.T) {
	raw, _ := os.ReadFile("testdata/scan.stdout")
	rowRE := regexp.MustCompile(`(?m)^\| https://osv\.dev/(\S+)\s+\| (\d+\.\d) `)
	want := 0
	for _, m := range rowRE.FindAllStringSubmatch(string(raw), -1) {
		if f, _ := strconv.ParseFloat(m[2], 64); f >= 7 {
			want++
		}
	}
	in, _ := load(t, "scan")
	got, _ := New().Aggressive(context.Background(), in)
	have := len(regexp.MustCompile(`(?m)^    \S+ (CRITICAL|HIGH)\b`).FindAllString(string(got.Body), -1))
	if want == 0 || have != want {
		t.Errorf("kept %d, raw has %d vulns with CVSS >= 7:\n%s", have, want, got.Body)
	}
	if !strings.Contains(string(got.Body), "PYSEC-2019-13 CRITICAL -> 2.2.4 aka GHSA-6r97-cj55-9hrq") {
		t.Errorf("alias or fixed version lost:\n%s", got.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{{"scan", "--format", "json"}, {"scan", "-f", "sarif"}, {"--format=markdown", "."}, {"scan", "--output", "x.json"}} {
		in, _ := load(t, argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "{\"results\":[]}", "| a | b |\n| 1 | 2 |\n", "No issues found\n"} {
		in := format.Input{Argv: []string{"osv-scanner", "scan"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "scan")
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
