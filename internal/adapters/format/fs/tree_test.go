package fs

import (
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// treeFixture is captured `tree` output over a small project (18 files, one
// directory with 15 children to exercise the per-directory cap).
const treeFixture = `.
├── README.md
└── src
    ├── app
    │   ├── file1.go
    │   ├── file10.go
    │   ├── file11.go
    │   ├── file12.go
    │   ├── file13.go
    │   ├── file14.go
    │   ├── file15.go
    │   ├── file2.go
    │   ├── file3.go
    │   ├── file4.go
    │   ├── file5.go
    │   ├── file6.go
    │   ├── file7.go
    │   ├── file8.go
    │   └── file9.go
    └── lib
        ├── a.go
        └── b.go

4 directories, 18 files
`

func TestTreeFormatterAggressive(t *testing.T) {
	f := &treeFormatter{}

	t.Run("strips box drawing and caps children per directory", func(t *testing.T) {
		out, err := f.Aggressive(testCtx, stdoutInput("tree", treeFixture))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)

		for _, boxChar := range []string{"├", "└", "│", "─"} {
			if strings.Contains(body, boxChar) {
				t.Errorf("expected box-drawing char %q to be stripped, got %q", boxChar, body)
			}
		}
		if !strings.Contains(body, "4 directories, 18 files") {
			t.Errorf("expected summary line preserved, got %q", body)
		}
		if !strings.Contains(body, "…+3 more") {
			t.Errorf("expected app/ (15 children) capped at 12 with a +3 marker, got %q", body)
		}
		if strings.Contains(body, "file9.go") {
			t.Errorf("expected children beyond the cap to be omitted, got %q", body)
		}
		if !strings.Contains(body, "  app") || !strings.Contains(body, "    file1.go") {
			t.Errorf("expected two-space indentation per depth, got %q", body)
		}
		if !shrunk(treeFixture, body) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(body), len(treeFixture))
		}
	})

	t.Run("small tree output is tier inapplicable", func(t *testing.T) {
		small := `.
├── a.go
└── b.go

0 directories, 2 files
`
		_, err := f.Aggressive(testCtx, stdoutInput("tree", small))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("empty output is tier inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(testCtx, stdoutInput("tree", ""))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestTreeFormatterRelaxed(t *testing.T) {
	f := &treeFormatter{}
	raw := strings.Repeat("├── dup.go\n", 3) + "└── other.go\n"
	out, err := f.Relaxed(testCtx, stdoutInput("tree", raw))
	if err != nil {
		t.Fatalf("Relaxed: %v", err)
	}
	body := string(out.Body)
	if strings.Count(body, "dup.go") != 1 {
		t.Errorf("expected consecutive duplicate lines deduped, got %q", body)
	}
	if out.FoldStderr {
		t.Error("Relaxed must not fold stderr for tree")
	}
}

func TestTreeCollapsesNoiseDirs(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(".\n├── app.js\n└── node_modules\n")
	for i := 1; i <= 30; i++ {
		sb.WriteString("    ├── pkg" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + "\n    │   └── index.js\n")
	}
	sb.WriteString("\n31 directories, 32 files\n")
	out, err := (&treeFormatter{}).Aggressive(testCtx, stdoutInput("tree", sb.String()))
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "node_modules …+60 entries (noise dir collapsed)") {
		t.Errorf("noise dir not collapsed with a descendant count: %q", body)
	}
	if strings.Contains(body, "index.js") {
		t.Errorf("noise contents leaked: %q", body)
	}
}
