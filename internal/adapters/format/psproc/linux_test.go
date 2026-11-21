package psproc

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* from ubuntu:24.04 (procps-ng), linux-busybox-* from
// alpine:3.20 (busybox ps: "PID USER TIME COMMAND", no BSD-style options).
// Both were captured with ~25 identical `sleep 600` children plus shell noise.

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
		name   string
		argv   []string
		keep   []string
		render bool
	}{
		{"ps-aux", []string{"ps", "aux"}, []string{"sleep 600 ×", "ps aux"}, true},
		{"ps-auxww", []string{"ps", "auxww"}, []string{"sleep 600 ×", "ps auxww"}, true},
		{"ps-ef", []string{"ps", "-ef"}, []string{"sleep 600 ×", "ps -ef"}, true},
		// Custom -o column lists are not a shape this parser knows; busybox
		// rejects procps column names outright (exit 1) and ps -ax is invalid.
		{"ps-eo", []string{"ps", "-eo", "pid,ppid,user,pcpu,pmem,args"}, nil, false},
		{"ps-ax", []string{"ps", "-ax"}, nil, false},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				in := func() format.Input {
					return format.Input{Argv: c.argv, Command: "ps", Stdout: strings.NewReader(raw), ExitCode: exit}
				}
				out, err := New().Aggressive(context.Background(), in())
				rendered := err == nil
				if os.Getenv("LINUX_DUMP") != "" {
					t.Logf("raw=%d err=%v\n%s", len(raw), err, out.Body)
				}
				shown := raw
				if rendered {
					shown = string(out.Body)
					if len(shown) >= len(raw) {
						t.Errorf("render not smaller: %d >= %d", len(shown), len(raw))
					}
					if exit != 0 {
						t.Errorf("rendered a non-zero exit")
					}
				}
				if c.render && !rendered {
					t.Fatalf("declined: %v", err)
				}
				if exit != 0 && !strings.Contains(shown, strings.SplitN(raw, "\n", 2)[0]) {
					t.Errorf("error line lost:\n%s", shown)
				}
				for _, k := range c.keep {
					if !strings.Contains(shown, k) {
						t.Errorf("lost %q:\n%s", k, shown)
					}
				}
				if rendered && !strings.Contains(shown, "processes") {
					t.Errorf("missing process count header:\n%s", shown)
				}
			})
		}
	}
}
