package du

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* from ubuntu:24.04 (coreutils 9.4), linux-busybox-*
// from alpine:3.20 (busybox 1.36, sizes print as "28.0K"), real output over a
// ~300-file tree with node_modules/vendor/.git noise.

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
		out, err := tier(context.Background(), format.Input{Argv: argv, Command: "du", Stdout: strings.NewReader(raw), ExitCode: exit})
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
		{"du-h", []string{"du", "-h", "."}, true, []string{"./src", "./node_modules", "./vendor", "./build", "./.git", "more paths"}},
		{"du-ah", []string{"du", "-ah", "src"}, true, []string{"src/pkg", "src/cmd/app/main.go"}},
		{"du-sh", []string{"du", "-sh", "README.md", "build", "docs", "logs", "node_modules", "src", "vendor"}, false, []string{"README.md", "node_modules", "vendor"}},
		{"du-s", []string{"du", "-s", "README.md", "build"}, false, nil},
		{"du-d1", []string{"du", "-h", "-d", "1", "."}, false, nil},
		{"du-sh-miss", []string{"du", "-sh", "nosuch"}, false, []string{"nosuch"}},
		{"du-maxdepth", []string{"du", "-h", "--max-depth=1", "."}, false, nil},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				body, rendered := chain(t, New(), c.argv, raw, exit)
				if os.Getenv("LINUX_DUMP") != "" {
					t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
				}
				out := raw
				if rendered {
					out = body
				}
				if exit != 0 {
					// The error signal survives whatever the tiers did.
					if first := strings.SplitN(raw, "\n", 2)[0]; !strings.Contains(out, first) {
						t.Fatalf("error line %q lost:\n%s", first, out)
					}
				}
				for _, k := range c.keep {
					if !strings.Contains(out, k) {
						t.Errorf("lost %q:\n%s", k, out)
					}
				}
				// du -sh over several operands is reordered by size, never
				// dropped: every operand's line must survive.
				if c.name == "du-sh" && rendered {
					for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
						if !strings.Contains(body, l) {
							t.Errorf("lost line %q", l)
						}
					}
				}
				if c.render && !rendered {
					t.Fatal("expected a smaller render")
				}
			})
		}
	}
}
