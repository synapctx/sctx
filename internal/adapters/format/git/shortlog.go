package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// shortlogCap is the number of contributors kept by `git shortlog`/
// `git shortlog -sn` before eliding the rest.
const shortlogCap = 20

// aggressiveShortlog caps `git shortlog -sn` (one "<count>  <author>" entry
// per line, already sorted by commit count) to the top shortlogCap
// contributors.
func aggressiveShortlog(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= shortlogCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	kept := lines[:shortlogCap]
	extra := len(lines) - shortlogCap
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d more contributors", extra)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d contributors (%d shown)", len(lines), shortlogCap),
	}, nil
}
