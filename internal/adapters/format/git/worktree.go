package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// worktreeCap is the number of worktrees kept by `git worktree list` before
// eliding the rest.
const worktreeCap = 20

// aggressiveWorktree handles `git worktree list` (one worktree per line),
// capping it to the first worktreeCap entries. Other `git worktree`
// subcommands (add/remove/prune) are left to the relaxed tier.
func aggressiveWorktree(in format.Input, args []string) (format.Rendered, error) {
	if len(args) == 0 || args[0] != "list" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= worktreeCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	kept := lines[:worktreeCap]
	extra := len(lines) - worktreeCap
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d more worktrees", extra)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d worktrees (%d shown)", len(lines), worktreeCap),
	}, nil
}
