package git

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveRemote(t *testing.T) {
	f := New()

	t.Run("dedups fetch/push pairs sharing a URL", func(t *testing.T) {
		raw := "origin\tgit@github.com:example/repo.git (fetch)\n" +
			"origin\tgit@github.com:example/repo.git (push)\n" +
			"upstream\tgit@github.com:upstream/repo.git (fetch)\n" +
			"upstream\tgit@github.com:upstream/repo.git (push)\n" +
			"fork\tgit@github.com:me/repo.git (fetch)\n" +
			"fork\tgit@github.com:me/repo-push.git (push)\n"
		in := format.Input{Argv: []string{"git", "remote", "-v"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "origin git@github.com:example/repo.git") {
			t.Errorf("body missing deduped origin line: %q", body)
		}
		if !strings.Contains(body, "fork fetch=git@github.com:me/repo.git push=git@github.com:me/repo-push.git") {
			t.Errorf("body missing distinct fetch/push line: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("small remote -v is inapplicable", func(t *testing.T) {
		raw := "origin\tgit@github.com:example/repo.git (fetch)\norigin\tgit@github.com:example/repo.git (push)\n"
		in := format.Input{Argv: []string{"git", "remote", "-v"}, Stdout: strings.NewReader(raw)}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("bare remote (no -v) is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"git", "remote"}, Stdout: strings.NewReader("origin\nupstream\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
