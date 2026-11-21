package helm

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

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	if e, err := os.ReadFile("testdata/" + name + ".exit"); err == nil && strings.TrimSpace(string(e)) != "0" {
		exit = 1
	}
	return string(b), exit
}

func in(argv []string, stdout, stderr string, exit int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
}

func TestSubcommand(t *testing.T) {
	for argv, want := range map[string]string{
		"helm install rel web --wait": "install", "helm -n x upgrade rel web": "upgrade",
		"helm repo update": "repo update", "helm dep update web": "dependency update",
		"helm get values rel": "get values", "helm list": "list", "helm": "",
	} {
		if got := subcommand(strings.Fields(argv)); got != want {
			t.Errorf("%q: got %q want %q", argv, got, want)
		}
	}
}

// Fixtures are REAL helm v4.3.2 output (a scaffolded chart installed into a
// kind cluster; macOS arm64); paths and repo names scrubbed.
func TestRealFixtures(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		must     []string
		mustNot  []string
		declines bool
	}{
		{name: "install", argv: []string{"helm", "install", "rel", "web", "--wait"},
			must:    []string{"STATUS: deployed", "REVISION: 1", "NOTES: …+"},
			mustNot: []string{"export POD_NAME"}},
		{name: "upgrade", argv: []string{"helm", "upgrade", "rel", "web"},
			must:    []string{`Release "rel" has been upgraded.`, "REVISION: 2", "DESCRIPTION: Upgrade complete", "NOTES: …+"},
			mustNot: []string{"port-forward"}},
		{name: "status", argv: []string{"helm", "status", "rel"},
			must: []string{"STATUS: deployed", "rel-web-8dfb5dc4-4t88p   1/1     Running", "NOTES: …+"}, mustNot: []string{"export POD_NAME"}},
		{name: "template", argv: []string{"helm", "template", "rel", "web"},
			must:    []string{"# Source: web/templates/deployment.yaml", "kind: Deployment", "app.kubernetes.io/instance: rel", "name: rel-web"},
			mustNot: []string{"helm.sh/chart", "managed-by: Helm"}},
		{name: "dryrun", argv: []string{"helm", "install", "rel", "web", "--dry-run=client"},
			must:    []string{"STATUS: pending-install", "MANIFEST:", "kind: Service", "NOTES: …+"},
			mustNot: []string{"helm.sh/chart"}},
		{name: "repoupdate", argv: []string{"helm", "repo", "update"},
			must:    []string{"Successfully got an update from 5 chart repositories: repo-a, repo-b, repo-c, repo-d, bitnami", "Update Complete."},
			mustNot: []string{"Hang tight"}},
		{name: "depupdate", argv: []string{"helm", "dependency", "update", "depweb"},
			must:    []string{"Saving 1 charts", "Downloading common from repo", "Deleting outdated charts", "Successfully got an update from 5 chart repositories"},
			mustNot: []string{"Hang tight"}},
		{name: "depupdate-fail", argv: []string{"helm", "dependency", "update", "depweb"},
			must: []string{`Error: can't get a valid version for 1 subchart(s): "common"`, "version \"99.0.0\""}},
		{name: "lint-ok", argv: []string{"helm", "lint", "web"}, must: []string{"==> Linting web", "1 chart(s) linted, 0 chart(s) failed"}, mustNot: []string{"icon is recommended"}},
		{name: "lint-bad", argv: []string{"helm", "lint", "broken"}, declines: false,
			must:    []string{"[ERROR] Chart.yaml: version 'not-semver' is not a valid SemVer", "[WARNING]", "Error: 1 chart(s) linted, 1 chart(s) failed"},
			mustNot: []string{"icon is recommended"}},
		// Nothing droppable: the tier steps aside and the caller sees it verbatim.

		{name: "install-bad", argv: []string{"helm", "install", "rel2", "broken"}, declines: true},
		{name: "template-bad", argv: []string{"helm", "template", "rel", "broken"}, declines: true},
		{name: "list", argv: []string{"helm", "list"}, declines: true},
		{name: "showvalues", argv: []string{"helm", "show", "values", "web"}, declines: true},
		{name: "package", argv: []string{"helm", "package", "web"}, declines: true},
		{name: "uninstall", argv: []string{"helm", "uninstall", "rel"}, declines: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, exit := load(t, tc.name)
			r, err := New().Aggressive(context.Background(), in(tc.argv, raw, "", exit))
			if tc.declines {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("want decline, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.must {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, w := range tc.mustNot {
				if strings.Contains(body, w) {
					t.Errorf("kept %q", w)
				}
			}
			if len(body) >= len(raw) || !r.Elided || !strings.HasPrefix(body, "…+") {
				t.Errorf("size %d/%d elided=%v", len(body), len(raw), r.Elided)
			}
			golden := filepath.Join("testdata", "golden", tc.name+".golden")
			if *update {
				_ = os.MkdirAll(filepath.Dir(golden), 0o755)
				if err := os.WriteFile(golden, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil || string(want) != body {
				t.Errorf("golden mismatch (run -update): %v", err)
			}
		})
	}
}

func TestRepoFailureLineKept(t *testing.T) {
	raw := "Hang tight while we grab the latest from your chart repositories...\n...Successfully got an update from the \"a\" chart repository\n...Unable to get an update from the \"b\" chart repository (https://x.example):\n\tGet \"https://x.example/index.yaml\": dial tcp: lookup x.example: no such host\n...Successfully got an update from the \"c\" chart repository\nUpdate Complete. ⎈Happy Helming!⎈\n"
	r, err := New().Aggressive(context.Background(), in([]string{"helm", "repo", "update"}, raw, "", 1))
	if err != nil {
		t.Fatal(err)
	}
	b := string(r.Body)
	if !strings.Contains(b, `Unable to get an update from the "b"`) || !strings.Contains(b, "no such host") || !strings.Contains(b, "2 chart repositories: a, c") {
		t.Fatalf("got:\n%s", b)
	}
}

func TestMachineOutputDeclines(t *testing.T) {
	raw, _ := load(t, "install")
	for _, argv := range [][]string{{"helm", "install", "r", "c", "-o", "json"}, {"helm", "status", "r", "--output=yaml"}, {"helm", "install", "--help"}} {
		if _, err := New().Aggressive(context.Background(), in(argv, raw, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}

func TestRelaxedKeepsNotes(t *testing.T) {
	raw, _ := load(t, "repoupdate")
	r, err := New().Relaxed(context.Background(), in([]string{"helm", "repo", "update"}, raw, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "5 chart repositories") {
		t.Fatal(string(r.Body))
	}
	raw, _ = load(t, "install")
	if _, err := New().Relaxed(context.Background(), in([]string{"helm", "install", "r", "c"}, raw, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("relaxed install must keep NOTES (decline), got %v", err)
	}
}
