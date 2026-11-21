package gitleaks

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Fixtures: gitleaks v8.28.0 (linux) detect/dir -v over a two-commit project
// holding fake credentials. The secret values in detect.stdout were replaced
// with x-runs after capture; the layout is untouched.

func load(t *testing.T, name string, argv ...string) (format.Input, int) {
	t.Helper()
	rd := func(ext string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(rd("exit")))
	o, e := rd("stdout"), rd("stderr")
	return format.Input{Argv: append([]string{"gitleaks"}, argv...), ExitCode: code,
		Stdout: strings.NewReader(o), Stderr: strings.NewReader(e)}, len(o) + len(e)
}

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"detect", []string{"detect", "--no-banner", "-v"}},
		{"redact", []string{"detect", "--no-banner", "-v", "--redact"}},
		{"dir", []string{"dir", "-v", "--redact", "secrets"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, rawLen := load(t, tc.name, tc.argv...)
			got, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(golden, got.Body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, _ := os.ReadFile(golden)
			if string(got.Body) != string(want) {
				t.Fatalf("golden mismatch:\n%s", got.Body)
			}
			if len(got.Body) >= rawLen {
				t.Fatalf("not smaller: %d >= %d", len(got.Body), rawLen)
			}
			if !got.FoldStderr || !got.Elided {
				t.Fatal("FoldStderr/Elided must be set")
			}
		})
	}
}

// The render must add no exposure: no credential material, entropy, email.
func TestNeverEmitsSecretMaterial(t *testing.T) {
	in, _ := load(t, "detect", "detect", "-v")
	got, _ := New().Aggressive(context.Background(), in)
	body := string(got.Body)
	for _, bad := range []string{"AKIA", "ghp_", "xoxb-", "xxxxxxxx", "Secret:", "Finding:", "Entropy", "dev@example.com", "\x1b"} {
		if strings.Contains(body, bad) {
			t.Errorf("render leaks %q:\n%s", bad, body)
		}
	}
}

func TestEveryLeakKept(t *testing.T) {
	in, _ := load(t, "detect", "detect", "-v")
	raw, _ := os.ReadFile("testdata/detect.stdout")
	want := strings.Count(string(raw), "RuleID:")
	got, _ := New().Aggressive(context.Background(), in)
	if n := strings.Count(string(got.Body), "\n  "); n != want {
		t.Errorf("kept %d of %d leaks:\n%s", n, want, got.Body)
	}
	for _, w := range []string{"hashicorp-tf-password", "tf/main.tf (", "leaks found: 6", "a5b5c0c3"} {
		if !strings.Contains(string(got.Body), w) {
			t.Errorf("missing %q:\n%s", w, got.Body)
		}
	}
}

func TestMultilineFindingContinuation(t *testing.T) {
	raw := "Finding:     -----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\n-----END RSA PRIVATE KEY-----\nSecret:      -----BEGIN RSA PRIVATE KEY-----\nMIIBOgIB\nRuleID:      private-key\nEntropy:     5.1\nFile:        id_rsa\nLine:        1\nFingerprint: id_rsa:private-key:1\n\n"
	in := format.Input{Argv: []string{"gitleaks", "dir", "-v"}, Stdout: strings.NewReader(raw), ExitCode: 1}
	got, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Body), "MIIB") || !strings.Contains(string(got.Body), "private-key") {
		t.Fatalf("bad render:\n%s", got.Body)
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"detect", "-f", "json", "-r", "-"}, {"detect", "--report-format=sarif", "-v"}, {"detect", "-v", "-r", "-"},
	} {
		in, _ := load(t, "detect", argv...)
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: want decline, got %v", argv, err)
		}
	}
	for _, s := range []string{"", "random text\nmore\n", "[{\"RuleID\":\"x\"}]"} {
		in := format.Input{Argv: []string{"gitleaks", "detect", "-v"}, Stdout: strings.NewReader(s)}
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%q: want decline, got %v", s, err)
		}
	}
	in, _ := load(t, "clean", "dir", "-v", "k8s") // no leaks: stdout empty, nothing to render
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("clean: %v", err)
	}
	if _, err := New().Relaxed(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("relaxed: %v", err)
	}
}
