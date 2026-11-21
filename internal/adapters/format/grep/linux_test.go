package grep

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* are GNU grep 3.11 + ripgrep 14.1 on ubuntu:24.04;
// linux-busybox-* are busybox grep 1.36 + ripgrep 14.1 on alpine:3.20. Real
// output over a ~300-file project tree (node_modules/vendor/.git present).

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

// materialize creates, under a fresh temp dir that becomes the working
// directory, an empty file for every "path:content" prefix in raw: the
// path-content parser validates its path tokens against the filesystem, and
// the fixtures were captured in a tree that does not exist on this machine.
func materialize(t *testing.T, raw string) {
	t.Helper()
	dir := t.TempDir()
	for _, l := range strings.Split(raw, "\n") {
		p, _, ok := strings.Cut(l, ":")
		if !ok || p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			continue
		}
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// chain runs aggressive then relaxed like the tier chain does.
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
		{"grep-rn", []string{"grep", "-rn", "TODO", "src"}, true, []string{"src/pkg/api/h0.go", "TODO handler 0", "…+20 more files (20 matches)"}},
		{"grep-r", []string{"grep", "-r", "FIXME", "src"}, true, []string{"src/pkg/store/s0.go", "FIXME 0"}},
		{"grep-rl", []string{"grep", "-rl", "TODO", "."}, true, []string{"docs/", "d1.md", "h59.go"}},
		{"grep-ctx", []string{"grep", "-n", "-C2", "ERROR", "logs/app.log"}, false, []string{"ERROR db timeout"}},
		{"grep-rn-ctx", []string{"grep", "-rn", "-C1", "TODO", "src/pkg/api"}, false, []string{"h0.go-5-", "h0.go:6:"}},
		{"grep-H", []string{"grep", "-Hn", "ERROR", "logs/app.log"}, false, []string{"logs/app.log:401:"}},
		{"grep-none", []string{"grep", "-rn", "zzzqq", "src"}, false, nil},
		{"grep-c", []string{"grep", "-c", "INFO", "logs/app.log"}, false, []string{"400"}},
		{"grep-rn-dirs", []string{"grep", "-n", "TODO", "src/cmd", "src/pkg", "docs/d1.md", "docs/d3.md"}, false, []string{"docs/d1.md:3:"}},
		{"rg-todo", []string{"rg", "TODO"}, true, []string{"docs/d1.md", "TODO write"}},
		{"rg-n", []string{"rg", "-n", "FIXME", "src"}, true, []string{"src/pkg/store/s9.go", "FIXME 9"}},
		{"rg-vimgrep", []string{"rg", "--vimgrep", "FIXME", "src"}, true, []string{"src/pkg/store/s9.go", "FIXME 9"}},
		{"rg-count", []string{"rg", "-c", "INFO", "logs"}, false, []string{"logs/app.log:400"}},
		{"rg-ctx-big", []string{"rg", "-n", "-C1", "TODO", "src/pkg/api"}, false, []string{"src/pkg/api/h0.go-5-"}},
		{"rg-none", []string{"rg", "zzzqq"}, false, nil},
		// rg --files prints a bare path list, regrouped by directory like -l.
		{"rg-files", []string{"rg", "--files"}, true, []string{"README.md", "docs/", "d1.md", "src/pkg/api/", "h59.go", "s59.go"}},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				materialize(t, raw)
				f := New()
				if c.argv[0] == "rg" {
					f = All()[1]
				}
				body, rendered := chain(t, f, c.argv, raw, exit)
				if os.Getenv("LINUX_DUMP") != "" {
					t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
				}
				if rendered != c.render {
					t.Fatalf("rendered=%v, want %v\n%s", rendered, c.render, body)
				}
				out := raw
				if rendered {
					out = body
				}
				if c.name == "grep-rn-dirs" && distro == "gnu" && !strings.Contains(out, "Is a directory") {
					t.Errorf("diagnostic lost:\n%s", out)
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

// A grep that fails on a busybox-only (or GNU-only) flag prints usage to
// stderr and exits 2; nothing of it may be rendered away.
func TestBusyboxUnknownOptionKeepsDiagnostic(t *testing.T) {
	raw, exit := linuxFixture(t, "busybox", "grep-rn-incl")
	if exit == 0 {
		t.Fatal("fixture should be a failure")
	}
	body, rendered := chain(t, New(), []string{"grep", "-rn", "--include=*.go", "FIXME", "."}, raw, exit)
	out := raw
	if rendered {
		out = body
	}
	if !strings.Contains(out, "unrecognized option: include=*.go") {
		t.Fatalf("diagnostic lost:\n%s", out)
	}
}

// rg --json is meant for a program: the relaxed tier used to cut long lines
// with an ellipsis, leaving invalid JSON records.
func TestMachineOutputIsNeverRewritten(t *testing.T) {
	for _, distro := range []string{"gnu", "busybox"} {
		raw, exit := linuxFixture(t, distro, "rg-json")
		for name, tier := range map[string]func(context.Context, format.Input) (format.Rendered, error){
			"aggressive": All()[1].Aggressive, "relaxed": All()[1].Relaxed,
		} {
			_, err := tier(context.Background(), format.Input{Argv: []string{"rg", "--json", "TODO", "docs"}, Stdout: strings.NewReader(raw), ExitCode: exit})
			if err == nil {
				t.Errorf("%s/%s rewrote machine output", distro, name)
			}
		}
	}
	for _, argv := range [][]string{{"grep", "-rlZ", "x", "."}, {"grep", "--null", "-rl", "x", "."}, {"rg", "-0", "-l", "x"}, {"rg", "--null-data", "x"}} {
		if !machineOutput(argv) {
			t.Errorf("%v not recognised as machine output", argv)
		}
	}
	for _, argv := range [][]string{{"grep", "-rn", "-m10", "x"}, {"rg", "-n", "x"}} {
		if machineOutput(argv) {
			t.Errorf("%v wrongly treated as machine output", argv)
		}
	}
}
