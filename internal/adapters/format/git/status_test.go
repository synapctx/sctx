package git

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const statusDirty = `On branch main
Changes to be committed:
  (use "git restore --staged <file>..." to unstage)
	new file:   c.txt

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   a.txt
	modified:   e.txt

Untracked files:
  (use "git add <file>..." to include in what will be committed)
	d.txt
`

const statusClean = `On branch main
nothing to commit, working tree clean
`

const statusAheadBehind = `On branch main
Your branch and 'origin/main' have diverged,
and have 2 and 3 different commits each, respectively.
  (use "git pull" to merge the remote branch into yours)

nothing to commit, working tree clean
`

func TestAggressiveStatus(t *testing.T) {
	f := New()

	t.Run("dirty tree groups staged/modified/untracked and drops hints", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "status"},
			Stdout: strings.NewReader(statusDirty),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if strings.Contains(body, "(use ") {
			t.Errorf("body still contains hint text: %q", body)
		}
		for _, want := range []string{"main", "staged (1): c.txt", "modified (2): a.txt, e.txt", "untracked (1): d.txt"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if len(out.Body) >= len(statusDirty) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(statusDirty))
		}
	})

	t.Run("clean tree collapses to a single line", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "status"},
			Stdout: strings.NewReader(statusClean),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if got, want := string(out.Body), "main: clean"; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})

	t.Run("diverged branch reports ahead/behind on line 1", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "status"},
			Stdout: strings.NewReader(statusAheadBehind),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "ahead 2") || !strings.Contains(body, "behind 3") {
			t.Errorf("body missing ahead/behind counts: %q", body)
		}
	})

	t.Run("empty stdout is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"git", "status"}, Stdout: strings.NewReader("")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("--short groups by status code and caps each group", func(t *testing.T) {
		var lines []string
		lines = append(lines, "M  staged.go")
		lines = append(lines, " M worktree.go")
		lines = append(lines, "R  old.go -> new.go")
		for i := 0; i < 35; i++ {
			lines = append(lines, "?? untracked"+strings.Repeat("x", i%3)+".txt")
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{
			Argv:   []string{"git", "status", "--short"},
			Stdout: strings.NewReader(raw),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{"staged (2): staged.go, old.go -> new.go", "modified (1): worktree.go", "untracked (35)"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if !strings.Contains(body, "…+") {
			t.Errorf("body missing elision marker for capped untracked group: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})
}
