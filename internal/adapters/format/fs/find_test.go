package fs

import (
	"fmt"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// findFixture is captured `find . -type f` output over a small tree.
const findFixture = `./README.md
./src/app/file1.go
./src/app/file10.go
./src/app/file11.go
./src/app/file12.go
./src/app/file13.go
./src/app/file14.go
./src/app/file15.go
./src/app/file2.go
./src/app/file3.go
./src/app/file4.go
./src/app/file5.go
./src/app/file6.go
./src/app/file7.go
./src/app/file8.go
./src/app/file9.go
./src/lib/a.go
./src/lib/b.go
`

func TestFindFormatterAggressive(t *testing.T) {
	f := &findFormatter{}

	t.Run("groups by parent directory and caps names per dir", func(t *testing.T) {
		out, err := f.Aggressive(testCtx, stdoutInput("find", findFixture))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if out.Note != "18 paths" {
			t.Errorf("Note = %q, want %q", out.Note, "18 paths")
		}
		if !strings.Contains(body, "src/: app 15, lib 2 [a.go, b.go]") {
			t.Errorf("expected folded parent with per-dir counts, got %q", body)
		}
		if !strings.Contains(body, "…+15 file names not listed") {
			t.Errorf("expected the 15-file dir to be count-only with an omitted-names marker, got %q", body)
		}
		if strings.Contains(body, "file1.go") {
			t.Errorf("large dir names should not be listed, got %q", body)
		}
		if !shrunk(findFixture, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(findFixture))
		}
	})

	t.Run("caps directories with a remaining-dirs marker", func(t *testing.T) {
		var b strings.Builder
		for i := range findDirsCap + 10 {
			for j := range 3 {
				fmt.Fprintf(&b, "./dir%02d/file%d.go\n", i, j)
			}
		}
		raw := b.String()

		out, err := f.Aggressive(testCtx, stdoutInput("find", raw))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if want := fmt.Sprintf("%d paths", 3*(findDirsCap+10)); out.Note != want {
			t.Errorf("Note = %q, want %q", out.Note, want)
		}
		if !strings.Contains(body, "…+10 more dirs (30 paths)") {
			t.Errorf("expected dir cap marker, got %q", body)
		}
		if !shrunk(raw, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(raw))
		}
		if strings.Count(body, "\n") > findDirsCap {
			t.Errorf("expected the directory listing to be capped, got %d lines", strings.Count(body, "\n")+1)
		}
	})

	t.Run("empty output is tier inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(testCtx, stdoutInput("find", ""))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("blank-only output is tier inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(testCtx, stdoutInput("find", "\n\n  \n"))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

// findLsFixture is captured `find . -ls` output over a small tree.
const findLsFixture = `12345      8 -rw-r--r--   1 user  staff      512 Jul  9 10:00 ./README.md
12346      4 -rw-r--r--   1 user  staff      128 Jul  9 10:01 ./src/app/file1.go
12347      4 -rw-r--r--   1 user  staff      256 Jul  9 10:02 ./src/app/file2.go
12348      4 -rw-r--r--   1 user  staff      340 Jul  9 10:03 ./src/lib/a.go
`

func TestFindFormatterAggressiveTable(t *testing.T) {
	f := &findFormatter{}

	t.Run("large default find output is grouped and capped", func(t *testing.T) {
		var b strings.Builder
		for i := range 5 {
			for j := range 40 {
				fmt.Fprintf(&b, "./pkg%02d/file%d.go\n", i, j)
			}
		}
		raw := b.String()

		out, err := f.Aggressive(testCtx, stdoutInput("find", raw))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if out.Note != "200 paths" {
			t.Errorf("Note = %q, want %q", out.Note, "200 paths")
		}
		if !strings.Contains(body, "pkg00 40") || !strings.Contains(body, "…+200 file names not listed") {
			t.Errorf("expected per-dir counts and an explicit omitted-names marker, got %q", body)
		}
		if !shrunk(raw, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(raw))
		}
	})

	t.Run("find -ls long listing is compacted", func(t *testing.T) {
		out, err := f.Aggressive(testCtx, stdoutInput("find", findLsFixture))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if out.Note != "4 paths" {
			t.Errorf("Note = %q, want %q", out.Note, "4 paths")
		}
		if !strings.Contains(body, "file1.go (128)") || !strings.Contains(body, "file2.go (256)") {
			t.Errorf("expected compacted name+size entries, got %q", body)
		}
		if strings.Contains(body, "user") || strings.Contains(body, "staff") || strings.Contains(body, "12345") {
			t.Errorf("expected inode/owner/group noise dropped, got %q", body)
		}
		if !shrunk(findLsFixture, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(findLsFixture))
		}
	})

	t.Run("small output is tier inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(testCtx, stdoutInput("find", "./a.go\n./b.go\n"))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("permission denied error line on stderr is never hidden", func(t *testing.T) {
		in := format.Input{
			Command: "find",
			Stdout:  strings.NewReader(findFixture),
			Stderr:  strings.NewReader("find: './private': Permission denied\n"),
		}
		out, err := f.Aggressive(testCtx, in)
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		if out.FoldStderr {
			t.Error("find must not fold stderr, so the pipeline re-emits it verbatim (error text must survive)")
		}
	})
}

func TestFindFormatterRelaxed(t *testing.T) {
	f := &findFormatter{}
	raw := strings.Repeat("./a.go\n", 3) + "./b.go\n"
	out, err := f.Relaxed(testCtx, stdoutInput("find", raw))
	if err != nil {
		t.Fatalf("Relaxed: %v", err)
	}
	body := string(out.Body)
	if strings.Count(body, "./a.go") != 1 {
		t.Errorf("expected consecutive duplicate lines deduped, got %q", body)
	}
	if out.FoldStderr {
		t.Error("Relaxed must not fold stderr for find")
	}
}

// TestFindGoRepoFixture runs a real `find . -name "*.go"` capture (a 600-file
// Go repository, 16.6 KB) through the formatter: every directory holding a
// match must appear with its count, and the render must stay tiny.
func TestFindGoRepoFixture(t *testing.T) {
	rawBytes, err := os.ReadFile("testdata/find-go.txt")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawBytes)
	out, err := (&findFormatter{}).Aggressive(testCtx, stdoutInput("find", raw))
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	t.Logf("raw=%d body=%d", len(raw), len(body))

	counts := map[string]int{}
	for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
		counts[path.Dir(l)]++
	}
	for d, n := range counts {
		leaf := path.Base(d)
		if d == "." {
			leaf = "."
		}
		if want := fmt.Sprintf("%s %d", leaf, n); !strings.Contains(body, want) {
			t.Errorf("dir %s missing %q", d, want)
		}
	}
	if !strings.Contains(body, fmt.Sprintf("%d paths in %d dirs", strings.Count(raw, "\n"), len(counts))) {
		t.Errorf("header missing totals: %q", strings.SplitN(body, "\n", 2)[0])
	}
	if len(body) > 1800 {
		t.Errorf("body %d bytes, want <= 1800", len(body))
	}
}

func TestFindCollapsesNoiseDirs(t *testing.T) {
	f := &findFormatter{}
	var sb strings.Builder
	sb.WriteString("./main.go\n./util/a.go\n./util/b.go\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "./web/node_modules/pkg%d/index.js\n./web/node_modules/pkg%d/lib/x.js\n", i, i)
	}
	sb.WriteString("./web/node_modules\n./.git/HEAD\n./.git/objects/ab/cdef\n")
	raw := sb.String()

	t.Run("noise dir becomes one counted line", func(t *testing.T) {
		out, err := f.Aggressive(testCtx, format.Input{Argv: []string{"find", ".", "-name", "*.js"}, Stdout: strings.NewReader(raw)})
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "web/node_modules/ …+60 paths in 60 dirs") {
			t.Errorf("missing counted node_modules line: %q", body)
		}
		if !strings.Contains(body, ".git/ …+2 paths in 2 dirs") {
			t.Errorf("missing counted .git line: %q", body)
		}
		if strings.Contains(body, "pkg1/") {
			t.Errorf("noise contents leaked: %q", body)
		}
		if !strings.Contains(body, "web 1 [node_modules]") || !strings.Contains(body, "util 2") {
			t.Errorf("non-noise dirs lost: %q", body)
		}
		if !strings.HasPrefix(body, "66 paths in") {
			t.Errorf("total path count must include collapsed paths: %q", body)
		}
	})

	t.Run("an explicit noise root is never collapsed", func(t *testing.T) {
		in := "web/node_modules/a/x.js\nweb/node_modules/a/y.js\nweb/node_modules/b/z.js\nweb/node_modules/b/w.js\n"
		out, err := f.Aggressive(testCtx, format.Input{Argv: []string{"find", "web/node_modules", "-name", "*.js"}, Stdout: strings.NewReader(in)})
		if err == nil && strings.Contains(string(out.Body), "noise dir collapsed") {
			t.Errorf("collapsed an explicitly requested root: %q", out.Body)
		}
	})
}
