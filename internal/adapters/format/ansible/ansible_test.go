package ansible

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

func in(argv, stdout, stderr string, exit int) format.Input {
	return format.Input{Argv: strings.Fields(argv), Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "ansible-playbook" {
		t.Fatal("descriptor")
	}
}

// Fixtures are REAL ansible-core output (playbooks run against localhost with
// ansible_connection=local, plus one unreachable ssh target); paths and user
// names scrubbed.
func TestRealFixtures(t *testing.T) {
	cases := []struct {
		name     string
		argv     string
		must     []string
		mustNot  []string
		declines bool
	}{
		{name: "ok", argv: "ansible-playbook -i inv.ini ok.yml",
			must: []string{"PLAY [local]", "TASK [Create scratch dir]", "changed: [localhost]", "changed: [localhost] => (item=c)",
				`ok: [localhost] => {"msg": "hello"}`, `ok: [localhost] => (item=4) => {"msg": "item 4"}`,
				"localhost                  : ok=6    changed=2    unreachable=0    failed=0    skipped=1"},
			mustNot: []string{"TASK [Skipped task]", "TASK [Run command]", "skipping:", "****"}},
		{name: "ok2", argv: "ansible-playbook ok.yml", must: []string{"PLAY RECAP", "changed=0"}, mustNot: []string{"TASK [Create scratch dir]"}},
		{name: "check", argv: "ansible-playbook ok.yml --check", must: []string{"PLAY RECAP"}},
		{name: "bad", argv: "ansible-playbook bad.yml",
			must: []string{"TASK [Failing command]", "[ERROR]: Task failed: Module failed: The command exited with a non-zero return code.",
				"Origin: /work/play/bad.yml:6:7", "fatal: [localhost]: FAILED!", `"rc": 3`, `"stderr": "to-stderr"`, "failed=1"},
			mustNot: []string{"TASK [never reached]"}},
		{name: "ignore", argv: "ansible-playbook ignore.yml",
			must: []string{"fatal: [localhost]: FAILED!", "...ignoring", "'nope' is undefined", "<<< caused by >>>", "ignored=2"}},
		{name: "unreach", argv: "ansible-playbook unreach.yml",
			must: []string{"fatal: [unreach]: UNREACHABLE!", "Connection refused", "unreachable=1"}},
		{name: "verbose", argv: "ansible-playbook ok.yml -v",
			must:    []string{`"stdout_lines": ["hi"]`, `ok: [localhost] => {"msg": "hello"}`},
			mustNot: []string{"skipping:"}},
		// Not run output: the tier steps aside and the caller sees it verbatim.
		{name: "syntax", argv: "ansible-playbook syntax.yml", declines: true},
		{name: "listtasks", argv: "ansible-playbook ok.yml --list-tasks", declines: true},
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

func TestRelaxedKeepsOkResults(t *testing.T) {
	raw, _ := load(t, "ok")
	r, err := New().Relaxed(context.Background(), in("ansible-playbook ok.yml", raw, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	b := string(r.Body)
	if !strings.Contains(b, "TASK [Run command]") || !strings.Contains(b, "ok: [localhost]\n") || strings.Contains(b, "skipping:") {
		t.Fatalf("relaxed wrong:\n%s", b)
	}
}

func TestStderrFolded(t *testing.T) {
	raw, _ := load(t, "ok")
	r, err := New().Aggressive(context.Background(), in("ansible-playbook ok.yml", raw, "[WARNING]: something\n", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !r.FoldStderr || !strings.Contains(string(r.Body), "[WARNING]: something") {
		t.Fatalf("stderr lost: %q", r.Body)
	}
}
