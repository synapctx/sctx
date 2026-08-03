package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// tagCap is the number of tags kept by `git tag` before eliding the rest.
const tagCap = 40

// aggressiveTag caps `git tag` (one tag name, or "<name> <annotation>" for
// `git tag -n`, per line) to the first tagCap entries.
func aggressiveTag(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= tagCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	kept := lines[:tagCap]
	extra := len(lines) - tagCap
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d more tags", extra)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d tags (%d shown)", len(lines), tagCap),
	}, nil
}
