package filediff

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* from GNU diffutils 3.10 on ubuntu:24.04 (timestamps in
// the ---/+++ headers), linux-busybox-* from busybox diff 1.36 on alpine:3.20
// (always unified, no timestamps). Exit 1 means "files differ", not failure.

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

func TestLinuxFixtures(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		keep []string // changed lines, headers and diagnostics: never dropped
		drop []string // context lines that may be elided (with a marker)
	}{
		{"diff-big", []string{"diff", "-u", "a.conf", "b.conf"}, []string{"-line 20 of the config file", "+LINE 20 of the config file", "-line 60 of the config file", "+LINE 60 of the config file", "+extra", "@@"}, nil},
		{"diff-U15", []string{"diff", "-U", "15", "a.conf", "b.conf"}, []string{"-line 20 of the config file", "+LINE 20 of the config file", "+extra"}, nil},
		{"diff-normal", []string{"diff", "a.conf", "b.conf"}, nil, nil},
		{"diff-ru", []string{"diff", "-ru", "d1", "d2"}, []string{"Only in d2: only.txt"}, nil},
		{"diff-ur", []string{"diff", "-ur", "src/pkg/api", "src/pkg/store"}, []string{"Only in src/pkg/api: h0.go", "Only in src/pkg/store: s59.go"}, nil},
		{"diff-miss", []string{"diff", "-u", "a.conf", "nosuch"}, []string{"nosuch", "No such file or directory"}, nil},
		{"diff-q", []string{"diff", "-q", "a.conf", "b.conf"}, []string{"differ"}, nil},
		{"diff-same", []string{"diff", "-u", "README.md", "README.md"}, nil, nil},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				out := raw
				for _, tier := range []func(context.Context, format.Input) (format.Rendered, error){New().Aggressive, New().Relaxed} {
					r, err := tier(context.Background(), format.Input{Argv: c.argv, Command: "diff", Stdout: strings.NewReader(raw), ExitCode: exit})
					if err == nil && len(r.Body) > 0 {
						out = string(r.Body)
						break
					}
				}
				if os.Getenv("LINUX_DUMP") != "" {
					t.Logf("raw=%d render=%d\n%s", len(raw), len(out), out)
				}
				for _, k := range c.keep {
					if !strings.Contains(out, k) {
						t.Errorf("lost %q:\n%s", k, out)
					}
				}
				// Every changed line of a unified diff survives verbatim.
				for _, l := range strings.Split(raw, "\n") {
					if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && !strings.Contains(out, l) {
						t.Errorf("changed line %q lost:\n%s", l, out)
					}
				}
			})
		}
	}
}
