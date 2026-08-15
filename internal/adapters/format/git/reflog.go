package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// reflogCap is the number of reflog entries kept before eliding the rest.
const reflogCap = 30

// aggressiveReflog caps `git reflog` (one "<hash> HEAD@{N}: <action>: <msg>"
// entry per line, newest first) to the most recent reflogCap entries.
func aggressiveReflog(in format.Input, args []string) (format.Rendered, error) {
	if hasCustomLineFormat(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= reflogCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	kept := lines[:reflogCap]
	extra := len(lines) - reflogCap
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d more", extra)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d entries (%d shown)", len(lines), reflogCap),
	}, nil
}
