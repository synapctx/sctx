package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// stashCap is the number of stash entries kept by `git stash list` before
// eliding the rest.
const stashCap = 20

// aggressiveStash handles `git stash list` (one "stash@{N}: ..." entry per
// line), capping it to the most recent stashCap entries. Other `git stash`
// subcommands (push/pop/apply/drop/show) are left to the relaxed tier.
func aggressiveStash(in format.Input, args []string) (format.Rendered, error) {
	if len(args) == 0 || args[0] != "list" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if hasCustomLineFormat(args[1:]) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= stashCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	kept := lines[:stashCap]
	extra := len(lines) - stashCap
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d more stashes", extra)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d stashes (%d shown)", len(lines), stashCap),
	}, nil
}
