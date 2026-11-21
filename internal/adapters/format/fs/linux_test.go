package fs

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures named linux-gnu-* (ubuntu:24.04: coreutils 9.4, procps, tree 2.1.1)
// and linux-busybox-* (alpine:3.20: busybox 1.36) are real captured output
// over a ~300-file project tree with node_modules/vendor/.git noise.

func linuxFixture(t *testing.T, distro, name string) (raw string, exit int) {
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

// renderChain mimics the tier chain: aggressive, then relaxed; ok=false means
// both declined and the raw output would pass through verbatim.
func renderChain(t *testing.T, f format.Formatter, argv []string, raw string, exit int) (string, bool) {
	t.Helper()
	for _, tier := range []func(format.Input) (format.Rendered, error){
		func(in format.Input) (format.Rendered, error) { return f.Aggressive(testCtx, in) },
		func(in format.Input) (format.Rendered, error) { return f.Relaxed(testCtx, in) },
	} {
		out, err := tier(format.Input{Argv: argv, Command: argv[0], Stdout: strings.NewReader(raw), ExitCode: exit})
		if err == nil && len(out.Body) > 0 && len(out.Body) < len(raw) && strings.TrimSpace(string(out.Body)) != strings.TrimSpace(raw) {
			return string(out.Body), true
		}
	}
	return "", false
}

func TestLinuxFixtures(t *testing.T) {
	type tc struct {
		name    string
		f       format.Formatter
		argv    []string
		render  bool
		keep    []string // must survive in the render (or raw when it declines)
		bbKeep  []string // busybox-only extras
		gnuKeep []string
	}
	cases := []tc{
		{name: "ls-la", f: &lsFormatter{}, argv: []string{"ls", "-la", "."}, render: true,
			keep: []string{".gitignore", "README.md", "build/", "docs/", "node_modules/", "src/", "vendor/"}},
		{name: "ls-la-src", f: &lsFormatter{}, argv: []string{"ls", "-la", "src/pkg/api"}, render: true,
			keep: []string{"h0.go", "h59.go"}},
		// Several operands print "dir:" headers even without -R; the plain
		// renderer used to flatten headers and columns into one comma list.
		{name: "ls-lh", f: &lsFormatter{}, argv: []string{"ls", "-lh", "build", "logs"}, render: true,
			keep: []string{"build/:", "blob.bin", "logs/:", "app.log", "dups.log", "events.jsonl"}},
		{name: "ls-multi-dirs", f: &lsFormatter{}, argv: []string{"ls", "docs", "logs"}, render: false,
			keep: []string{"docs:", "logs:"}},
		{name: "ls-multi-files", f: &lsFormatter{}, argv: []string{"ls", "-l", "README.md", ".gitignore", "logs/app.log"}, render: false},
		// Sticky-bit directories (/tmp is drwxrwxrwt) must still parse.
		{name: "ls-ls", f: &lsFormatter{}, argv: []string{"ls", "-lah", "/"}, render: true,
			keep: []string{"tmp/", "usr/"}, gnuKeep: []string{"bin -> usr/bin"}},
		{name: "ls-lt", f: &lsFormatter{}, argv: []string{"ls", "-lt", "logs"}, render: true, keep: []string{"app.log", "dups.log"}},
		{name: "ls-R", f: &lsFormatter{}, argv: []string{"ls", "-R", "src", "docs"}, render: true,
			keep: []string{"src/pkg/api/:", "h0.go", "docs/:", "d1.md"}},
		{name: "ls-miss", f: &lsFormatter{}, argv: []string{"ls", "-la", "nosuchdir"}, render: false},
		{name: "find-plain", f: &findFormatter{}, argv: []string{"find", ".", "-not", "-path", "./.git/*"}, render: true,
			keep: []string{"blob.bin", "node_modules/"}},
		{name: "find-type-f", f: &findFormatter{}, argv: []string{"find", ".", "-type", "f", "-not", "-path", "./.git/*"}, render: true,
			keep: []string{"blob.bin", "main.go"}},
		{name: "find-name", f: &findFormatter{}, argv: []string{"find", ".", "-name", "*.go", "-not", "-path", "./node_modules/*"}, render: true,
			keep: []string{"main.go"}},
		{name: "find-miss", f: &findFormatter{}, argv: []string{"find", "nosuch", "-name", "x"}, render: false},
		{name: "wc-l", f: &wcFormatter{}, argv: []string{"wc", "-l", "src/pkg/api/h0.go"}, render: true,
			keep: []string{"h0.go", "h59.go", "total"}},
		{name: "wc-miss", f: &wcFormatter{}, argv: []string{"wc", "-l", "nosuch"}, render: false},
		{name: "df-h", f: &dfFormatter{}, argv: []string{"df", "-h"}, render: true, keep: []string{"Mounted-on", "/work"}},
		{name: "df-hT", f: &dfFormatter{}, argv: []string{"df", "-hT"}, render: true, keep: []string{"Mounted-on", "/work", "fuse.grpcfuse"}},
		{name: "df-plain", f: &dfFormatter{}, argv: []string{"df"}, render: true, keep: []string{"/work"}},
		{name: "lsof-i-Pn", f: &lsofFormatter{}, argv: []string{"lsof", "-i", "-P", "-n"}, render: false, keep: []string{"8080", "9090", "LISTEN"}},
		{name: "lsof-listen", f: &lsofFormatter{}, argv: []string{"lsof", "-nP", "-iTCP", "-sTCP:LISTEN"}, render: false, keep: []string{"9090", "LISTEN"}},
		{name: "tree-plain", f: &treeFormatter{}, argv: []string{"tree", "-I", ".git", "."}, render: true,
			keep: []string{"README.md", "blob.bin", "d1.md", "main.go", "node_modules", "directories,", "files"}},
		{name: "tree-L2", f: &treeFormatter{}, argv: []string{"tree", "-L", "2", "-a", "-I", ".git"}, render: true,
			keep: []string{".gitignore", "README.md", "docs", "d8.md", "app.log", "12 directories, 15 files"}},
	}
	for _, distro := range []string{"gnu", "busybox"} {
		for _, c := range cases {
			t.Run(distro+"/"+c.name, func(t *testing.T) {
				raw, exit := linuxFixture(t, distro, c.name)
				body, rendered := renderChain(t, c.f, c.argv, raw, exit)
				if os.Getenv("LINUX_DUMP") != "" {
					t.Logf("raw=%d render=%d rendered=%v\n%s", len(raw), len(body), rendered, body)
				}
				if rendered != c.render {
					t.Fatalf("rendered=%v, want %v\n%s", rendered, c.render, body)
				}
				out := raw
				if rendered {
					out = body
				}
				keep := append([]string{}, c.keep...)
				if distro == "gnu" {
					keep = append(keep, c.gnuKeep...)
				} else {
					keep = append(keep, c.bbKeep...)
				}
				for _, k := range keep {
					if !strings.Contains(out, k) {
						t.Errorf("lost %q:\n%s", k, out)
					}
				}
			})
		}
	}
}

// An ASCII-charset tree (non-UTF-8 locale, the default in minimal Linux
// containers) used to parse as marker-less lines: every entry was dropped
// and only the "N directories, M files" summary survived.
func TestTreeASCIICharsetKeepsEntries(t *testing.T) {
	raw, _ := linuxFixture(t, "gnu", "tree-plain")
	if !strings.Contains(raw, "|-- ") || strings.Contains(raw, "├") {
		t.Fatal("fixture is expected to be the ASCII charset")
	}
	body, ok := renderChain(t, &treeFormatter{}, []string{"tree", "-I", ".git", "."}, raw, 0)
	if !ok {
		t.Fatal("ASCII tree declined")
	}
	for _, want := range []string{"  build\n    blob.bin", "  docs\n    d1.md", "…+48 more"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
}

// A tree line the parser cannot place must decline rather than silently drop.
func TestTreeUnparsableLineDeclines(t *testing.T) {
	var b strings.Builder
	b.WriteString(".\n")
	for i := 0; i < 30; i++ {
		b.WriteString("+-- f\n")
	}
	b.WriteString("\n1 directories, 30 files\n")
	_, err := (&treeFormatter{}).Aggressive(testCtx, format.Input{Argv: []string{"tree"}, Stdout: strings.NewReader(b.String())})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("unknown charset: err = %v, want ErrTierInapplicable", err)
	}
}

func TestLinuxEnvAndLsofNeverLoseVariables(t *testing.T) {
	for _, distro := range []string{"gnu", "busybox"} {
		raw, exit := linuxFixture(t, distro, "env")
		body, ok := renderChain(t, &envFormatter{}, []string{"env"}, raw, exit)
		out := raw
		if ok {
			out = body
		}
		for _, k := range []string{"CI=true", "GOPATH=/go", "DATABASE_URL=", "AWS_REGION=us-east-1", "PATH="} {
			if !strings.Contains(out, k) {
				t.Errorf("%s env lost %q:\n%s", distro, k, out)
			}
		}
	}
}
