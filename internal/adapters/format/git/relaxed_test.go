package git

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("drops hints, advice, progress and collapses blank runs", func(t *testing.T) {
		raw := `On branch main
Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)


	modified:   a.txt

hint: this is just advice
Counting objects: 100% (5/5), done.
no changes added to commit
`
		in := format.Input{
			Argv:   []string{"git", "status"},
			Stdout: strings.NewReader(raw),
		}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		for _, unwanted := range []string{"(use ", "hint:", "Counting objects"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("body still contains %q: %q", unwanted, body)
			}
		}
		if strings.Contains(body, "\n\n\n") {
			t.Errorf("blank-line runs not collapsed: %q", body)
		}
		if !strings.Contains(body, "modified:   a.txt") {
			t.Errorf("body missing substantive line: %q", body)
		}
	})

	t.Run("failure declines so native diagnostics remain verbatim", func(t *testing.T) {
		raw := `fatal: not a git repository
error: something went wrong
warning: be careful
CONFLICT (content): Merge conflict in a.txt
 ! [rejected] main -> main
`
		in := format.Input{
			Argv:     []string{"git", "merge"},
			Stdout:   strings.NewReader(raw),
			ExitCode: 1,
		}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("never returns empty body for non-empty raw input", func(t *testing.T) {
		raw := "Counting objects: 100% (5/5), done.\n"
		in := format.Input{
			Argv:   []string{"git", "push"},
			Stderr: strings.NewReader(raw),
		}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if len(out.Body) == 0 {
			t.Error("body is empty for non-empty raw input")
		}
	})

	t.Run("empty input is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"git", "status"}}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
