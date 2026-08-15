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
func aggressiveShortlog(in format.Input, args []string) (format.Rendered, error) {
	if hasCustomLineFormat(args) || !shortlogSummary(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
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

func shortlogSummary(args []string) bool {
	for _, arg := range args {
		if arg == "--summary" {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsRune(arg[1:], 's') {
			return true
		}
	}
	return false
}
