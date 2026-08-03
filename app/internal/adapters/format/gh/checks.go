package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveChecks renders `gh pr checks` output into one compact line per
// check ("<name> <state>"), preceded by a count/failure summary line.
func aggressiveChecks(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	type check struct{ name, state string }
	var checks []check
	failing := 0

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var cols []string
		if strings.Contains(line, "\t") {
			cols = strings.Split(line, "\t")
		} else {
			cols = strings.Fields(line)
		}
		if len(cols) < 2 {
			continue
		}
		name := strings.TrimSpace(cols[0])
		state := strings.TrimSpace(cols[1])
		if strings.EqualFold(state, "fail") || strings.EqualFold(state, "failure") {
			failing++
		}
		checks = append(checks, check{name, state})
	}
	if len(checks) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d checks (%d failing)", len(checks), failing)
	for _, c := range checks {
		fmt.Fprintf(&b, "\n%s %s", c.name, c.state)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}
