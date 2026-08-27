package pip

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func input(argv []string, stdout, stderr string, exitCode int) format.Input {
	return format.Input{
		Argv:     argv,
		Command:  "pip",
		Stdout:   strings.NewReader(stdout),
		Stderr:   strings.NewReader(stderr),
		ExitCode: exitCode,
	}
}

func TestAll(t *testing.T) {
	fs := All()
	if len(fs) != 2 {
		t.Fatalf("All() = %d formatters, want 2", len(fs))
	}
	if got := fs[0].Descriptor().Command; got != "pip" {
		t.Errorf("fs[0].Descriptor().Command = %q, want pip", got)
	}
	if got := fs[1].Descriptor().Command; got != "pip3" {
		t.Errorf("fs[1].Descriptor().Command = %q, want pip3", got)
	}
}

func TestAggressive(t *testing.T) {
	f := New()
	ctx := context.Background()

	tests := []struct {
		name       string
		argv       []string
		stdout     string
		stderr     string
		exitCode   int
		wantErr    error
		wantSubstr []string // all must appear in Body
		wantNotSub []string // none may appear in Body
	}{
		{
			name: "install with noise",
			argv: []string{"pip", "install", "requests"},
			stdout: strings.Join([]string{
				"Collecting requests",
				"  Downloading requests-2.31.0-py3-none-any.whl (62 kB)",
				"Requirement already satisfied: charset-normalizer in /x (from requests) (3.2.0)",
				"Requirement already satisfied: idna in /x (from requests) (3.4)",
				"Requirement already satisfied: urllib3 in /x (from requests) (2.0.4)",
				"Requirement already satisfied: certifi in /x (from requests) (2023.7.22)",
				"Installing collected packages: requests",
				"Successfully installed requests-2.31.0",
			}, "\n"),
			exitCode:   0,
			wantSubstr: []string{"Successfully installed requests-2.31.0", "…+"},
			wantNotSub: []string{"Downloading requests", "Requirement already satisfied"},
		},
		{
			name: "install failure keeps error",
			argv: []string{"pip", "install", "nonexistent-pkg-xyz"},
			stdout: strings.Join([]string{
				"Collecting nonexistent-pkg-xyz",
			}, "\n"),
			stderr: strings.Join([]string{
				"ERROR: Could not find a version that satisfies the requirement nonexistent-pkg-xyz",
				"ERROR: No matching distribution found for nonexistent-pkg-xyz",
			}, "\n"),
			exitCode:   1,
			wantSubstr: []string{"ERROR: Could not find a version", "ERROR: No matching distribution"},
		},
		{
			name: "list capped",
			argv: []string{"pip", "list"},
			stdout: func() string {
				lines := []string{"Package    Version", "---------- -------"}
				for i := range 70 {
					lines = append(lines, "pkg"+padInt(i)+"      1.0."+padInt(i))
				}
				return strings.Join(lines, "\n")
			}(),
			exitCode:   0,
			wantSubstr: []string{"70 packages", "…+10 more", "pkg00==1.0.00"},
		},
		{
			name: "show",
			argv: []string{"pip", "show", "requests"},
			stdout: strings.Join([]string{
				"Name: requests",
				"Version: 2.31.0",
				"Summary: Python HTTP for Humans.",
				"Home-page: https://requests.readthedocs.io",
				"Author: Kenneth Reitz",
				"Author-email: me@kennethreitz.org",
				"License: Apache 2.0",
				"Location: /x/site-packages",
				"Requires: charset-normalizer, idna, urllib3, certifi",
				"Required-by: ",
			}, "\n"),
			exitCode:   0,
			wantSubstr: []string{"Name: requests", "Version: 2.31.0", "Requires: charset-normalizer, idna, urllib3, certifi"},
			wantNotSub: []string{"Summary:", "Home-page:", "Author:", "License:", "Location:"},
		},
		{
			name:     "non-pip blob is inapplicable",
			argv:     []string{"pip", "download", "requests"},
			stdout:   "Saved ./requests-2.31.0-py3-none-any.whl\n",
			exitCode: 0,
			wantErr:  format.ErrTierInapplicable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := input(tt.argv, tt.stdout, tt.stderr, tt.exitCode)
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
		"Collecting requests",
		"Requirement already satisfied: idna in /x (3.4)",
		"Requirement already satisfied: idna in /x (3.4)",
		"45%|████████            | 1.2/2.7 MB 3.1 MB/s eta 0:00:01",
		"[notice] A new release of pip is available",
		"ERROR: something went wrong",
	}, "\n")
	got, err := f.Relaxed(ctx, input([]string{"pip", "install", "x"}, stdout, "", 1))
	if err != nil {
		t.Fatalf("Relaxed() err = %v", err)
	}
	body := string(got.Body)
	if !strings.Contains(body, "ERROR: something went wrong") {
		t.Errorf("Relaxed body dropped ERROR line: %s", body)
	}
	if strings.Contains(body, "45%|") || strings.Contains(body, "[notice]") {
		t.Errorf("Relaxed body kept noise: %s", body)
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
