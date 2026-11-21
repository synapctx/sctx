package fs

import (
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// lsLongFixture is captured `ls -la` output over a small tree.
const lsLongFixture = `total 0
drwxr-xr-x   4 devuser           wheel   128  6 Jul 22:31 .
drwx------  48 devuser           wheel  1536  6 Jul 22:31 ..
-rw-r--r--   1 devuser           wheel     0  6 Jul 22:31 README.md
drwxr-xr-x   4 devuser           wheel   128  6 Jul 22:31 src
`

// lsPlainFixture mirrors `ls -Cp` output in a wide terminal: dirs marked
// with a trailing slash, space-padded multi-column layout (as coreutils/BSD
// ls emits when column-aligning to a tty).
const lsPlainFixture = "dir_alpha/        dir_kappa/        dir_sigma/        file_b.go         file_i.go  \n" +
	"dir_beta/         dir_lambda/       dir_tau/          file_c.go         file_j.go  \n" +
	"dir_delta/        dir_mu/           dir_theta/        file_d.go         file_k.go  \n" +
	"dir_epsilon/      dir_nu/           dir_upsilon/      file_e.go         file_l.go  \n" +
	"dir_eta/          dir_omicron/      dir_xi/           file_f.go         file_m.go  \n" +
	"dir_gamma/        dir_pi/           dir_zeta/         file_g.go         file_n.go  \n" +
	"dir_iota/         dir_rho/          file_a.go         file_h.go         file_o.go  \n"

func TestLsFormatterAggressive(t *testing.T) {
	f := &lsFormatter{}

	t.Run("long format compresses to name size modified", func(t *testing.T) {
		out, err := f.Aggressive(testCtx, stdoutInput("ls", lsLongFixture))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		if len(out.Body) == 0 {
			t.Fatal("expected non-empty body")
		}
		if len(out.Body) >= len(lsLongFixture) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(out.Body), len(lsLongFixture))
		}
		body := string(out.Body)
		if !strings.Contains(body, "src/") {
			t.Errorf("expected directory marked with trailing slash, got %q", body)
		}
		if strings.Contains(body, "README.md/") {
			t.Errorf("file should not be marked as directory: %q", body)
		}
		if out.Note != "2 entries" {
			t.Errorf("Note = %q, want %q", out.Note, "2 entries")
		}
		// "total" and "." / ".." lines must not leak into the body.
		if strings.Contains(body, "total") {
			t.Errorf("body should not contain the total line: %q", body)
		}
	})

	t.Run("plain listing re-flows comma separated, dirs first", func(t *testing.T) {
		out, err := f.Aggressive(testCtx, stdoutInput("ls", lsPlainFixture))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		if out.Note != "35 entries" {
			t.Errorf("Note = %q, want %q", out.Note, "35 entries")
		}
		firstDirIdx := strings.Index(body, "dir_alpha/")
		firstFileIdx := strings.Index(body, "file_a.go")
		if firstDirIdx == -1 || firstFileIdx == -1 || firstDirIdx > firstFileIdx {
			t.Errorf("expected directories to sort before files, got %q", body)
		}
		if len(out.Body) >= len(lsPlainFixture) {
			t.Fatalf("body not smaller than raw: body=%d raw=%d", len(out.Body), len(lsPlainFixture))
		}
	})

	t.Run("tiny output is tier inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(testCtx, stdoutInput("ls", "a.go\nb.go\n"))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("empty output is tier inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(testCtx, stdoutInput("ls", ""))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestLsFormatterRelaxed(t *testing.T) {
	f := &lsFormatter{}
	raw := "a.go\na.go\na.go\nb.go\nc.go\n"
	out, err := f.Relaxed(testCtx, stdoutInput("ls", raw))
	if err != nil {
		t.Fatalf("Relaxed: %v", err)
	}
	body := string(out.Body)
	if strings.Count(body, "a.go") != 1 {
		t.Errorf("expected consecutive duplicate lines deduped, got %q", body)
	}
	if out.FoldStderr {
		t.Error("Relaxed must not fold stderr for ls")
	}
}

func TestLsRecursiveSections(t *testing.T) {
	raw := "a\nb\nnode_modules\n\n./a:\nx.go\ny.go\n\n./b:\n\n./node_modules:\np1\np2\n\n./node_modules/p1:\nindex.js\n\n./node_modules/p2:\nindex.js\n"
	in := format.Input{Argv: []string{"ls", "-R"}, Stdout: strings.NewReader(raw)}
	out, err := (&lsFormatter{}).Aggressive(testCtx, in)
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{"./: a b node_modules", "a/: x.go y.go", "b/: (empty)", "node_modules/ …+4 entries in 3 dirs"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "index.js") {
		t.Errorf("noise contents leaked: %q", body)
	}

	t.Run("a non-section recursive listing is never flattened", func(t *testing.T) {
		in := format.Input{Argv: []string{"ls", "-R"}, Stdout: strings.NewReader("a\nb\n\nstray\nline\n\nmore\nstuff\n")}
		if _, err := (&lsFormatter{}).Aggressive(testCtx, in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestLsRecursiveCaps(t *testing.T) {
	var b strings.Builder
	b.WriteString("top\n")
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&b, "\n./d%02d:\n", i)
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&b, "file%02d.go\n", j)
		}
	}
	out, err := (&lsFormatter{}).Aggressive(testCtx, format.Input{Argv: []string{"ls", "-R"}, Stdout: strings.NewReader(b.String())})
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "…+11 more dirs (440 entries) not listed") {
		t.Errorf("missing remaining-dirs marker: %q", body[len(body)-200:])
	}
	if !strings.Contains(body, "file29.go …+10\n") {
		t.Errorf("missing per-dir names marker")
	}
}
