package npm

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxListLines caps how many rows of a list/ls/outdated columnar or tree
// listing are kept verbatim before eliding the rest.
const maxListLines = 40

// aggressiveList caps large columnar/tree output from list/ls/outdated. It
// only activates once the listing exceeds maxListLines, since a small
// listing carries no compressible noise; smaller ones fall through to the
// relaxed/verbatim tiers unchanged.
func aggressiveList(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) <= maxListLines {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	shown, extra := lines[:maxListLines], len(lines)-maxListLines
	var b strings.Builder
	b.WriteString(strings.Join(shown, "\n"))
	fmt.Fprintf(&b, "\n…+%d more", extra)

	return format.Rendered{Body: []byte(b.String())}, nil
}
