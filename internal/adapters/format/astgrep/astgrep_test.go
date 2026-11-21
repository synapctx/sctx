package astgrep

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestScanKeepsEveryFinding(t *testing.T) {
	cases := []struct {
		file     string
		findings int
		keep     []string
	}{
		{"scan-warn.txt", 3, []string{"a.js:2:3: warning[no-console]: Do not use console.log", "a.js:6:19: warning[no-console]"}},
		{"scan-mixed.txt", 9, []string{"c.js:6:10: error[no-console-error]: console.error is banned", " = use the logger", "a.js:3:3: warning[no-if-block]: avoid braces-if", "c.js:2:3: warning[no-if-block]", "Error: 2 error(s) found in code.", "Help: Scan succeeded and found error level diagnostics"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			for _, f := range All() {
				out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{f.Descriptor().Command, "scan"}, Stdout: strings.NewReader(raw), ExitCode: 1})
				if err != nil {
					t.Fatal(err)
				}
				body := string(out.Body)
				if len(body) >= len(raw) {
					t.Errorf("not smaller %d >= %d", len(body), len(raw))
				}
				for _, k := range tc.keep {
					if !strings.Contains(body, k) {
						t.Errorf("lost %q:\n%s", k, body)
					}
				}
				if n := strings.Count(body, "]: "); n != tc.findings {
					t.Errorf("findings = %d, want %d:\n%s", n, tc.findings, body)
				}
				if strings.ContainsAny(body, "│╭") || !strings.Contains(body, "…+") {
					t.Errorf("excerpt left or elision unmarked:\n%s", body)
				}
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	scan := fixture(t, "scan-warn.txt")
	for _, argv := range [][]string{{"ast-grep", "scan", "--json"}, {"ast-grep", "scan", "--report-style", "short"}, {"ast-grep", "scan", "--format=github"}} {
		if _, err := All()[0].Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(scan)}); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
	if _, err := All()[0].Aggressive(context.Background(), format.Input{Argv: []string{"ast-grep", "scan"}, Stdout: strings.NewReader(fixture(t, "run.txt"))}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("non-scan shape: %v", err)
	}
}
