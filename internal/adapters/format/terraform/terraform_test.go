package terraform

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
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

func input(argv []string, stdout, stderr string, exit int) format.Input {
	return format.Input{
		Argv: argv, Command: strings.Join(argv, " "),
		Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit,
	}
}

func TestDescriptorAll(t *testing.T) {
	var got []string
	for _, f := range All() {
		got = append(got, f.Descriptor().Command)
	}
	if strings.Join(got, ",") != "terraform,tofu" {
		t.Fatalf("got %v", got)
	}
}

func TestPlanCapKeepsReplacementReason(t *testing.T) {
	var b strings.Builder
	b.WriteString("aws_x.y: Refreshing state... [id=1]\n\n  # aws_x.y must be replaced\n-/+ resource \"aws_x\" \"y\" {\n")
	for i := 0; i < 400; i++ {
		b.WriteString("      + attr" + strings.Repeat("x", i%3) + " = \"v\"\n")
	}
	b.WriteString("      ~ ami = \"a\" -> \"b\" # forces replacement\n    }\n\nPlan: 1 to add, 0 to change, 1 to destroy.\n")
	r, err := New().Aggressive(context.Background(), input([]string{"tofu", "plan"}, b.String(), "", 0))
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if !strings.Contains(body, "forces replacement") || !strings.Contains(body, "…+") ||
		!strings.Contains(body, "attributes") || !strings.Contains(body, "Plan: 1 to add") ||
		!strings.Contains(body, "# aws_x.y must be replaced") {
		t.Fatalf("bad render:\n%s", body)
	}
	if strings.Count(body, "\n") > 40 {
		t.Errorf("cap not applied: %d lines", strings.Count(body, "\n"))
	}
}

func TestStderrFolded(t *testing.T) {
	stdout := "aws_x.y: Refreshing state... [id=1]\nNo changes. Your infrastructure matches the configuration.\n"
	stderr := "\nError: boom\n\n  on main.tf line 1\n"
	r, err := New().Aggressive(context.Background(), input([]string{"terraform", "plan"}, stdout, stderr, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !r.FoldStderr || !strings.Contains(string(r.Body), "Error: boom") || !strings.Contains(string(r.Body), "No changes.") {
		t.Fatalf("got %q", r.Body)
	}
}

func TestInitSummary(t *testing.T) {
	raw := fixture(t, "tf-init.txt")
	r, err := New().Aggressive(context.Background(), input([]string{"terraform", "init"}, raw, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	want := "terraform init: ok (providers hashicorp/local v2.9.1, hashicorp/null v3.3.2, hashicorp/random v3.9.1)\n"
	if string(r.Body) != want {
		t.Fatalf("got %q", r.Body)
	}
	t.Logf("init before=%d after=%d", len(raw), len(r.Body))
}

// An error-only init has nothing droppable: every tier steps aside so the
// diagnostic reaches the caller byte for byte.
func TestInitErrorDeclinesEveryTier(t *testing.T) {
	raw := fixture(t, "tfbad-init-nocfg.txt")
	in := func() format.Input { return input([]string{"terraform", "init"}, raw, "", 1) }
	if _, err := New().Aggressive(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("aggressive err=%v", err)
	}
	if _, err := New().Relaxed(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("relaxed err=%v", err)
	}
}

func TestPassthroughSubcommandsDecline(t *testing.T) {
	for _, argv := range [][]string{
		{"terraform", "validate"}, {"terraform", "fmt"}, {"tofu", "show"},
		{"terraform", "plan", "-json"}, {"terraform", "apply", "-help"},
	} {
		for name, fn := range map[string]func(context.Context, format.Input) (format.Rendered, error){
			"aggressive": New().Aggressive, "relaxed": New().Relaxed,
		} {
			if _, err := fn(context.Background(), input(argv, "Success! The configuration is valid.\n", "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("%v %s: err=%v", argv, name, err)
			}
		}
	}
}

func TestRelaxedKeepsPlanBody(t *testing.T) {
	raw := fixture(t, "tf-plan-change.txt")
	r, err := New().Relaxed(context.Background(), input([]string{"terraform", "plan"}, raw, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if !strings.Contains(body, "# (3 unchanged attributes hidden)") || strings.Contains(body, "Refreshing state") {
		t.Fatalf("relaxed wrong:\n%s", body)
	}
}

func TestNoNoiseDeclines(t *testing.T) {
	if _, err := New().Aggressive(context.Background(), input([]string{"terraform", "plan"}, "something unrelated\n", "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := New().Aggressive(context.Background(), input([]string{"terraform", "plan"}, "", "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err=%v", err)
	}
}

var update = flag.Bool("update", false, "rewrite golden files")

func exitOf(t *testing.T, name string) int {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".exit")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) == "0" {
		return 0
	}
	return 1
}

// TestRealFixtures drives REAL terraform 1.x and OpenTofu 1.x output (captured
// on macOS arm64, stdout+stderr merged) through the aggressive tier and checks
// golden renders, the error signal of failing runs, and that nothing the agent
// needs to decide on is dropped.
func TestRealFixtures(t *testing.T) {
	cases := []struct {
		name     string
		program  string
		sub      string
		must     []string
		mustNot  []string
		declines bool
	}{
		{name: "tf-plan", program: "terraform", sub: "plan",
			must:    []string{"# local_file.f[\"alpha\"] will be created", "Plan: 8 to add, 0 to change, 0 to destroy.", "Changes to Outputs:", "known after apply"},
			mustNot: []string{"Resource actions are indicated", "Refreshing state"}},
		{name: "tf-plan-change", program: "terraform", sub: "plan",
			must: []string{"# random_pet.p[\"gamma\"] must be replaced", "# forces replacement", "EOT -> (known after apply)", "Plan: 7 to add, 0 to change, 7 to destroy."}},
		{name: "tf-plan-nochange", program: "terraform", sub: "plan",
			must: []string{"No changes. Your infrastructure matches the configuration."}, mustNot: []string{"Refreshing state"}},
		{name: "tf-apply", program: "terraform", sub: "apply",
			must:    []string{"Apply complete! Resources: 8 added, 0 changed, 0 destroyed.", "null_resource.n (local-exec): hi", "pw = <sensitive>"},
			mustNot: []string{"Creation complete after", ": Creating..."}},
		{name: "tf-apply-change", program: "terraform", sub: "apply", must: []string{"Apply complete!"}},
		{name: "tf-destroy", program: "terraform", sub: "destroy",
			must: []string{"Destroy complete! Resources: 8 destroyed.", "will be destroyed"}},
		{name: "tf-init", program: "terraform", sub: "init",
			must: []string{"terraform init: ok", "hashicorp/random v3.9.1", "hashicorp/local v2.9.1"}},
		{name: "tofu-init", program: "tofu", sub: "init",
			must: []string{"tofu init: ok", "hashicorp/random v3.9.1"}},
		{name: "tofu-plan", program: "tofu", sub: "plan", must: []string{"Plan: 8 to add"}, mustNot: []string{"OpenTofu used the selected providers"}},
		{name: "tofu-plan-change", program: "tofu", sub: "plan", must: []string{"must be replaced", "Plan: 7 to add, 0 to change, 7 to destroy."}},
		{name: "tofu-plan-nochange", program: "tofu", sub: "plan", must: []string{"No changes."}},
		{name: "tofu-apply", program: "tofu", sub: "apply", must: []string{"Apply complete! Resources: 8 added"}},
		{name: "tofu-destroy", program: "tofu", sub: "destroy", must: []string{"Destroy complete!"}},
		{name: "tfbad-apply-fail", program: "terraform", sub: "apply",
			must: []string{"Error: Create local file error", "Error: local-exec provisioner error", "read-only file system", "exit status 3", "main.tf line 16"}},
		{name: "tofubad-apply-fail", program: "tofu", sub: "apply",
			must: []string{"Error: Create local file error", "Error: local-exec provisioner error", "exit status 3"}},
		{name: "tfbad-plan-nocolor", program: "terraform", sub: "plan", must: []string{"Plan: "}},
		// Short outputs with nothing to drop: the tier must step aside.
		{name: "tf-fmt", program: "terraform", sub: "fmt", declines: true},
		{name: "tf-output", program: "terraform", sub: "output", declines: true},
		{name: "tf-statelist", program: "terraform", sub: "state", declines: true},
		{name: "tf-providers", program: "terraform", sub: "providers", declines: true},
		{name: "tf-validate", program: "terraform", sub: "validate", declines: true},
		{name: "tfbad-validate-bad", program: "terraform", sub: "validate", declines: true},
		{name: "tfbad-init-nocfg", program: "terraform", sub: "init", declines: true},
		{name: "tfbad-plan-novar", program: "terraform", sub: "plan", declines: true},
		{name: "tofubad-plan-novar", program: "tofu", sub: "plan", declines: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := fixture(t, tc.name+".txt")
			exit := exitOf(t, tc.name)
			f := &Formatter{command: tc.program}
			r, err := f.Aggressive(context.Background(), input([]string{tc.program, tc.sub}, raw, "", exit))
			if tc.declines {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("want decline, got err=%v body=%q", err, r.Body)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			plain := ansiRE.ReplaceAllString(body, "")
			for _, w := range tc.must {
				if !strings.Contains(plain, w) {
					t.Errorf("missing %q in:\n%s", w, plain)
				}
			}
			for _, w := range tc.mustNot {
				if strings.Contains(plain, w) {
					t.Errorf("kept %q", w)
				}
			}
			if len(body) >= len(raw) {
				t.Errorf("render not smaller: %d >= %d", len(body), len(raw))
			}
			if !r.Elided || !strings.Contains(body, "…+") && !strings.Contains(body, "init: ok") {
				t.Errorf("elision not marked (elided=%v)", r.Elided)
			}
			golden := filepath.Join("testdata", "golden", tc.name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden (run -update): %v", err)
			}
			if string(want) != body {
				t.Errorf("golden mismatch for %s (run -update to review)", tc.name)
			}
		})
	}
}

// A failed run keeps its per-resource completion lines: they say what DID get
// applied before the error.
func TestFailureKeepsCompletionLines(t *testing.T) {
	raw := fixture(t, "tfbad-apply-fail.txt")
	r, err := New().Aggressive(context.Background(), input([]string{"terraform", "apply"}, raw, "", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "null_resource.a: Creation complete after 0s") {
		t.Fatalf("completion line dropped on failure:\n%s", r.Body)
	}
}

func TestMachineOutputDeclines(t *testing.T) {
	for _, a := range [][]string{{"terraform", "plan", "-json"}, {"tofu", "apply", "-json"}, {"terraform", "plan", "-help"}} {
		_, err := New().Aggressive(context.Background(), input(a, "x", "", 0))
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", a, err)
		}
	}
}
