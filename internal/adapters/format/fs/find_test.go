package fs

import (
	"fmt"
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
		if !strings.Contains(body, "src/app/ (15):") {
			t.Errorf("expected grouped dir header with count, got %q", body)
		}
		if !strings.Contains(body, "…+5") {
			t.Errorf("expected 15-name dir to cap at 10 with a +5 marker, got %q", body)
		}
		if !strings.Contains(body, "src/lib/ (2): a.go, b.go") {
			t.Errorf("expected small dir listed in full, got %q", body)
		}
		if !shrunk(findFixture, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(findFixture))
		}
	})

	t.Run("caps directories at 60 with a remaining-dirs marker", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 70; i++ {
			for j := 0; j < 3; j++ {
				fmt.Fprintf(&b, "./dir%02d/file%d.go\n", i, j)
			}
		}
		raw := b.String()

		out, err := f.Aggressive(testCtx, stdoutInput("find", raw))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if out.Note != "210 paths" {
			t.Errorf("Note = %q, want %q", out.Note, "210 paths")
		}
		if !strings.Contains(body, "…+10 more dirs (30 paths)") {
			t.Errorf("expected dir cap marker, got %q", body)
		}
		if !shrunk(raw, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(raw))
		}
		if strings.Count(body, "\n") >= 70 {
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
		for i := 0; i < 5; i++ {
			for j := 0; j < 40; j++ {
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
		if !strings.Contains(body, "…+30 more files in pkg00") {
			t.Errorf("expected explicit elision marker naming the directory, got %q", body)
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
