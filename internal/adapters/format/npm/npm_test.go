package npm

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func input(command string, argv []string, stdout, stderr string, exitCode int) format.Input {
	return format.Input{
		Argv:     argv,
		Command:  command,
		Stdout:   strings.NewReader(stdout),
		Stderr:   strings.NewReader(stderr),
		ExitCode: exitCode,
	}
}

func TestAll(t *testing.T) {
	fs := All()
	if len(fs) != 3 {
		t.Fatalf("All() = %d formatters, want 3", len(fs))
	}
	want := []string{"npm", "pnpm", "yarn"}
	for i, w := range want {
		if got := fs[i].Descriptor().Command; got != w {
			t.Errorf("fs[%d].Descriptor().Command = %q, want %q", i, got, w)
		}
	}
}

func TestAggressive(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		formatter  string
		argv       []string
		stdout     string
		stderr     string
		exitCode   int
		wantErr    error
		wantSubstr []string
		wantNotSub []string
	}{
		{
			name:      "npm install with deprecation and funding noise",
			formatter: "npm",
			argv:      []string{"npm", "install"},
			stdout: strings.Join([]string{
				"npm WARN deprecated request@2.88.2: request has been deprecated",
				"npm WARN deprecated har-validator@5.1.5: this library is no longer supported",
				"npm WARN deprecated uuid@3.4.0: Please upgrade to a maintained version",
				"npm WARN deprecated core-js@2.6.12: core-js@<3.4 is no longer maintained",
				"",
				"added 142 packages, and audited 143 packages in 8s",
				"",
				"20 packages are looking for funding",
				"  run `npm fund` for details",
			}, "\n"),
			exitCode: 0,
			wantSubstr: []string{
				"added 142 packages, and audited 143 packages in 8s",
				"npm WARN deprecated request@2.88.2",
				"npm WARN deprecated har-validator@5.1.5",
				"npm WARN deprecated uuid@3.4.0",
				"…+1 more deprecation warnings",
				"…+",
			},
			wantNotSub: []string{
				"core-js@2.6.12",
				"looking for funding",
				"npm fund",
			},
		},
		{
			name:      "npm ci failure keeps error",
			formatter: "npm",
			argv:      []string{"npm", "ci"},
			stdout:    "",
			stderr: strings.Join([]string{
				"npm ERR! code ETARGET",
				"npm ERR! notarget No matching version found for left-pad@99.99.99.",
			}, "\n"),
			exitCode: 1,
			wantSubstr: []string{
				"npm ERR! code ETARGET",
				"npm ERR! notarget No matching version found for left-pad@99.99.99.",
			},
		},
		{
			name:      "pnpm install summary",
			formatter: "pnpm",
			argv:      []string{"pnpm", "install"},
			stdout: strings.Join([]string{
				"Progress: resolved 10, reused 8, downloaded 2, added 0",
				"+ lodash 4.17.21",
				"+ chalk 5.3.0",
				"Packages: +2",
				"++",
				"Done in 1.2s",
			}, "\n"),
			exitCode: 0,
			wantSubstr: []string{
				"Packages: +2",
				"Done in 1.2s",
				"…+",
			},
			wantNotSub: []string{
				"Progress: resolved",
				"+ lodash 4.17.21",
			},
		},
		{
			name:      "yarn add",
			formatter: "yarn",
			argv:      []string{"yarn", "add", "left-pad"},
			stdout: strings.Join([]string{
				"yarn add v1.22.19",
				"[1/4] Resolving packages...",
				"[2/4] Fetching packages...",
				"[3/4] Linking dependencies...",
				"[4/4] Building fresh packages...",
				"success Saved lockfile.",
				"Done in 0.85s.",
			}, "\n"),
			exitCode: 0,
			wantSubstr: []string{
				"success Saved lockfile.",
				"Done in 0.85s.",
			},
			wantNotSub: []string{
				"Resolving packages",
				"Fetching packages",
			},
		},
		{
			name:      "npm audit summary",
			formatter: "npm",
			argv:      []string{"npm", "audit"},
			stdout: strings.Join([]string{
				"# npm audit report",
				"",
				"minimist  <1.2.6",
				"Severity: critical",
				"Prototype Pollution - https://example.com/advisories/1179",
				"fix available via `npm audit fix`",
				"node_modules/minimist",
				"",
				"lodash  <4.17.21",
				"Severity: high",
				"Prototype Pollution - https://example.com/advisories/1523",
				"fix available via `npm audit fix`",
				"node_modules/lodash",
				"",
				"y18n  <=4.0.0",
				"Severity: moderate",
				"Prototype Pollution - https://example.com/advisories/1500",
				"node_modules/y18n",
				"",
				"axios  <0.21.2",
				"Severity: moderate",
				"Server-Side Request Forgery - https://example.com/advisories/1594",
				"node_modules/axios",
				"",
				"5 vulnerabilities (2 moderate, 1 high, 1 critical, 1 low)",
			}, "\n"),
			exitCode: 1,
			wantSubstr: []string{
				"5 vulnerabilities (2 moderate, 1 high, 1 critical, 1 low)",
				"minimist  <1.2.6",
				"…+1 more advisories",
			},
			wantNotSub: []string{
				"axios  <0.21.2",
			},
		},
		{
			name:      "npm test strips wrapper lines",
			formatter: "npm",
			argv:      []string{"npm", "test"},
			stdout: strings.Join([]string{
				"",
				"> myapp@1.0.0 test",
				"> jest",
				"",
				"PASS ./index.test.js",
				"Tests:       1 passed, 1 total",
			}, "\n"),
			exitCode: 0,
			wantSubstr: []string{
				"PASS ./index.test.js",
				"Tests:       1 passed, 1 total",
			},
			wantNotSub: []string{
				"> myapp@1.0.0 test",
				"> jest",
			},
		},
		{
			name:      "npm ls capped",
			formatter: "npm",
			argv:      []string{"npm", "ls"},
			stdout: func() string {
				var lines []string
				for i := range 60 {
					lines = append(lines, "├── pkg"+padInt(i)+"@1.0.0")
				}
				return strings.Join(lines, "\n")
			}(),
			exitCode: 0,
			wantSubstr: []string{
				"…+20 more",
				"pkg00@1.0.0",
			},
			wantNotSub: []string{
				"pkg59@1.0.0",
			},
		},
		{
			name:      "non-npm blob is inapplicable",
			formatter: "npm",
			argv:      []string{"npm", "config", "get", "registry"},
			stdout:    "https://registry.npmjs.org/\n",
			exitCode:  0,
			wantErr:   format.ErrTierInapplicable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Formatter{command: tt.formatter}
			in := input(tt.formatter, tt.argv, tt.stdout, tt.stderr, tt.exitCode)
			got, err := f.Aggressive(ctx, in)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("Aggressive() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Aggressive() unexpected err = %v", err)
			}
			body := string(got.Body)
			for _, s := range tt.wantSubstr {
				if !strings.Contains(body, s) {
					t.Errorf("Body missing %q; got:\n%s", s, body)
				}
			}
			for _, s := range tt.wantNotSub {
				if strings.Contains(body, s) {
					t.Errorf("Body unexpectedly contains %q; got:\n%s", s, body)
				}
			}
		})
	}
}

func TestRelaxedRetainsErrorsAndDedupes(t *testing.T) {
	f := New()
	ctx := context.Background()
	stdout := strings.Join([]string{
		"npm warn deprecated request@2.88.2: request has been deprecated",
		"npm warn deprecated request@2.88.2: request has been deprecated",
		"20 packages are looking for funding",
		"npm ERR! code ERESOLVE",
	}, "\n")
	got, err := f.Relaxed(ctx, input("npm", []string{"npm", "install", "--force"}, stdout, "", 1))
	if err != nil {
		t.Fatalf("Relaxed() err = %v", err)
	}
	body := string(got.Body)
	if !strings.Contains(body, "npm ERR! code ERESOLVE") {
		t.Errorf("Relaxed body dropped ERR line: %s", body)
	}
	if strings.Contains(body, "looking for funding") {
		t.Errorf("Relaxed body kept funding noise: %s", body)
	}
	if !strings.Contains(body, "×2") {
		t.Errorf("Relaxed body missing dedupe marker: %s", body)
	}
}

func padInt(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
