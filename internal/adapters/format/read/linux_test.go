package read

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* from ubuntu:24.04 coreutils 9.4, linux-busybox-* from
// alpine:3.20 busybox 1.36; both over the same generated logs (an
// application log, a 60-record JSONL file and an 80x repeated WARN log).

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

func chain(t *testing.T, f format.Formatter, argv []string, raw string, exit int) (string, bool) {
	t.Helper()
	for _, tier := range []func(context.Context, format.Input) (format.Rendered, error){f.Aggressive, f.Relaxed} {
		out, err := tier(context.Background(), format.Input{Argv: argv, Command: argv[0], Stdout: strings.NewReader(raw), ExitCode: exit})
		if err == nil && len(out.Body) > 0 && len(out.Body) < len(raw) && strings.TrimSpace(string(out.Body)) != strings.TrimSpace(raw) {
			return string(out.Body), true
		}
	}
	return "", false
}

func TestLinuxFixtures(t *testing.T) {
	cases := []struct {
		name   string
		argv   []string
		render bool
		keep   []string
	}{
		{"cat-dups", []string{"cat", "logs/dups.log"}, true, []string{"WARN retrying connection to db:5432 ×80"}},
		{"tail-dups", []string{"tail", "-n", "60", "logs/dups.log"}, true, []string{"WARN retrying connection to db:5432 ×60"}},
		{"cat-jsonl", []string{"cat", "logs/events.jsonl"}, true, []string{`"id":0`, "more JSON records"}},
		// Prose-like content, multi-file banners and errors pass untouched.
		{"head-def", []string{"head", "logs/app.log"}, false, []string{"id=0 ", "id=9 "}},
		{"tail-def", []string{"tail", "logs/app.log"}, false, []string{"ERROR db timeout id=7"}},
		{"head-multi", []string{"head", "-n", "3", "README.md", "logs/app.log"}, false, []string{"==> README.md <==", "==> logs/app.log <==", "# proj"}},
		{"tail-multi", []string{"tail", "-n", "3", "README.md", "logs/app.log"}, false, []string{"==> README.md <==", "ERROR db timeout id=7"}},
		{"cat-miss", []string{"cat", "nosuch"}, false, []string{"nosuch"}},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				var f format.Formatter
				for _, cand := range All() {
					if cand.Descriptor().Command == c.argv[0] {
						f = cand
					}
				}
				body, rendered := chain(t, f, c.argv, raw, exit)
				if rendered != c.render {
					t.Fatalf("rendered=%v, want %v\n%s", rendered, c.render, body)
				}
				out := raw
				if rendered {
					out = body
				}
				for _, k := range c.keep {
					if !strings.Contains(out, k) {
						t.Errorf("lost %q:\n%s", k, out)
					}
				}
			})
		}
	}
}
