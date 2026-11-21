package git

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* from git 2.43 on ubuntu:24.04, linux-busybox-* from git
// 2.45 on alpine:3.20, taken in a throwaway repo (7 commits, a feature branch,
// a staged add+modify, an unstaged delete+modify and untracked files).

func linuxFixture(t *testing.T, distro, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/linux-" + distro + "-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile("testdata/linux-" + distro + "-" + name + ".exit")
	if err != nil {
		t.Fatal(err)
	}
	return string(b), int(e[0] - '0')
}

// tiers returns what the chain would emit: the first tier that renders
// something non-empty, else the raw bytes.
func tiers(t *testing.T, argv []string, raw string, exit int) (string, bool) {
	t.Helper()
	f := New()
	for _, tier := range []func(context.Context, format.Input) (format.Rendered, error){f.Aggressive, f.Relaxed} {
		out, err := tier(context.Background(), format.Input{Argv: argv, Command: "git", Stdout: strings.NewReader(raw), ExitCode: exit})
		if err == nil && len(out.Body) > 0 && len(out.Body) < len(raw) {
			return string(out.Body), true
		}
	}
	return raw, false
}

func TestLinuxFixtures(t *testing.T) {
	hash := regexp.MustCompile(`(?m)^commit ([0-9a-f]{40})`)
	cases := []struct {
		name string
		argv []string
		keep []string
	}{
		// A deletion must not be reported as a plain modification.
		{"git-status", []string{"git", "status"}, []string{"master", "docs/d3.md", "src/staged_new.go", "docs/d2.md (deleted)", "src/pkg/api/h1.go", "logs/dups.log", "logs/events.jsonl", "src/new.go"}},
		{"git-status-s", []string{"git", "status", "-s"}, []string{"docs/d3.md", "src/staged_new.go", "docs/d2.md", "src/pkg/api/h1.go", "src/new.go"}},
		{"git-log", []string{"git", "log"}, []string{"tweak handler 5", "tweak handler 0", "initial import"}},
		{"git-log-n", []string{"git", "log", "-n", "5"}, []string{"tweak handler 5", "tweak handler 1"}},
		{"git-log-oneline", []string{"git", "log", "--oneline"}, []string{"tweak handler 5", "initial import"}},
		{"git-log-stat", []string{"git", "log", "--stat", "-n", "3"}, []string{"tweak handler 5", "src/pkg/api/h5.go"}},
		{"git-log-graph", []string{"git", "log", "--oneline", "--graph", "--decorate", "--all"}, []string{"feature work", "initial import"}},
		{"git-diff", []string{"git", "diff"}, []string{"docs/d2.md", "src/pkg/api/h1.go", "+// wip"}},
		{"git-diff-big", []string{"git", "diff", "HEAD~6", "HEAD"}, []string{"+// change 0"}},
		{"git-diff-stat", []string{"git", "diff", "--stat"}, []string{"docs/d2.md", "src/pkg/api/h1.go"}},
		{"git-diff-cached", []string{"git", "diff", "--cached"}, []string{"docs/d3.md", "src/staged_new.go"}},
		{"git-branch", []string{"git", "branch"}, []string{"feature/x", "* master"}},
		{"git-branch-a", []string{"git", "branch", "-a"}, []string{"feature/x", "* master"}},
		{"git-branch-v", []string{"git", "branch", "-v"}, []string{"feature/x", "* master", "feature work", "tweak handler 5"}},
		{"git-show", []string{"git", "show", "--stat", "HEAD"}, []string{"tweak handler 5", "src/pkg/api/h5.go"}},
		{"git-blame", []string{"git", "blame", "README.md"}, []string{"# proj"}},
		{"git-fatal", []string{"git", "checkout", "nosuchbranch"}, []string{"pathspec 'nosuchbranch' did not match"}},
		{"git-lsfiles", []string{"git", "ls-files"}, []string{"README.md", "src/cmd/app/ (1): main.go"}},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				out, rendered := tiers(t, c.argv, raw, exit)
				if os.Getenv("LINUX_DUMP") != "" {
					t.Logf("raw=%d render=%d rendered=%v\n%s", len(raw), len(out), rendered, out)
				}
				for _, k := range c.keep {
					if !strings.Contains(out, k) {
						t.Errorf("lost %q:\n%s", k, out)
					}
				}
				// Every commit survives a log rendering, by short hash.
				if c.name == "git-log" || c.name == "git-log-n" || c.name == "git-log-stat" {
					for _, m := range hash.FindAllStringSubmatch(raw, -1) {
						if !strings.Contains(out, m[1][:7]) {
							t.Errorf("commit %s lost:\n%s", m[1][:7], out)
						}
					}
				}
				if strings.HasPrefix(c.name, "git-diff") && c.name != "git-diff-stat" {
					for _, l := range strings.Split(raw, "\n") {
						if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && !strings.Contains(out, l) {
							t.Errorf("changed line %q lost", l)
						}
					}
				}
				if exit != 0 && !strings.Contains(out, "did not match") {
					t.Errorf("error signal lost:\n%s", out)
				}
			})
		}
	}
}

// A deleted file grouped under "modified" must still read as a deletion, in
// both the long and the --short forms.
func TestStatusMarksDeletions(t *testing.T) {
	long, _ := linuxFixture(t, "gnu", "git-status")
	out, ok := tiers(t, []string{"git", "status"}, long, 0)
	if !ok || !strings.Contains(out, "docs/d2.md (deleted)") || strings.Contains(out, "docs/d3.md (deleted)") {
		t.Fatalf("long form:\n%s", out)
	}
	short := " D docs/d2.md\nM  docs/d3.md\n M src/pkg/api/h1.go\nD  gone.txt\n" + untrackedList()
	body, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"git", "status", "--short"}, Stdout: strings.NewReader(short)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs/d2.md (deleted)", "gone.txt (deleted)"} {
		if !strings.Contains(string(body.Body), want) {
			t.Errorf("short form lost %q:\n%s", want, body.Body)
		}
	}
	if strings.Contains(string(body.Body), "h1.go (deleted)") {
		t.Errorf("modified file reported as deleted:\n%s", body.Body)
	}
}

func untrackedList() string {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "?? some/long/untracked/dir/file%d.txt\n", i)
	}
	return b.String()
}
