package helmfile

import (
	"context"
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

func run(t *testing.T, tier func(context.Context, format.Input) (format.Rendered, error), argv []string, out, errOut string, exit int) (format.Rendered, error) {
	t.Helper()
	return tier(context.Background(), format.Input{
		Argv: argv, Stdout: strings.NewReader(out), Stderr: strings.NewReader(errOut), ExitCode: exit,
	})
}

func TestTemplateDropsProgressAndBoilerplate(t *testing.T) {
	out, errOut := fixture(t, "template.stdout"), fixture(t, "template.stderr")
	r, err := run(t, New().Aggressive, []string{"helmfile", "template"}, out, errOut, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if len(body) >= len(out)+len(errOut) {
		t.Fatalf("no shrink: %d >= %d", len(body), len(out)+len(errOut))
	}
	t.Logf("template raw=%d rendered=%d", len(out)+len(errOut), len(body))
	for _, want := range []string{"kind: Deployment", "kind: Service", "…+4 helmfile progress lines", "chart labels"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Building dependency release=") || strings.Contains(body, "managed-by: Helm") {
		t.Errorf("boilerplate survived:\n%s", body)
	}
	if !r.FoldStderr {
		t.Error("stderr must be folded")
	}
}

func TestFailureKeepsEveryErrorLine(t *testing.T) {
	errOut := fixture(t, "diff-cluster-unreachable.stderr")
	for name, tier := range map[string]func(context.Context, format.Input) (format.Rendered, error){
		"aggressive": New().Aggressive, "relaxed": New().Relaxed,
	} {
		t.Run(name, func(t *testing.T) {
			r, err := run(t, tier, []string{"helmfile", "diff"}, "", errOut, 1)
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, want := range []string{
				"in ./helmfile.yaml: 2 errors:", "err 0:", "err 1:",
				"Kubernetes cluster unreachable", `plugin "diff" exited with error`,
				"EXIT STATUS", "ARGS: helm diff upgrade --allow-unreleased demo2 demo --namespace apps --reset-values",
				"/tmp/helmfile3504170644/apps-demo-values-69cffb5fb9",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("lost %q", want)
				}
			}
			if strings.Contains(body, "(4 bytes)") {
				t.Error("ARGS byte-count lines not joined")
			}
			if len(body) >= len(errOut) {
				t.Errorf("no shrink: %d >= %d", len(body), len(errOut))
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		out  string
		err  string
	}{
		{"machine output", []string{"helmfile", "list", "--output", "json"}, "[]", "Building dependency release=a, chart=b\n"},
		{"no subcommand", []string{"helmfile"}, "x", ""},
		{"nothing to compact", []string{"helmfile", "list"}, fixture(t, "lint.stdout"), ""},
		{"build output", []string{"helmfile", "build"}, "filepath: helmfile.yaml\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := run(t, New().Aggressive, tt.argv, tt.out, tt.err, 0); err != format.ErrTierInapplicable {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSubcommandSkipsGlobalFlagValues(t *testing.T) {
	if got := subcommand([]string{"helmfile", "-f", "x.yaml", "-e", "prod", "template"}); got != "template" {
		t.Fatalf("subcommand = %q", got)
	}
}
