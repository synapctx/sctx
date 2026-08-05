package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveRunList renders `gh run list` TSV output into one compact line
// per run: "<status> <conclusion> <workflow> <branch> <age>", preceded by a
// count/failure summary line.
func aggressiveRunList(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	type run struct{ status, conclusion, workflow, branch, age string }
	var runs []run
	failed := 0

	for _, line := range lines {
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "\t") {
			continue
		}
		cols := strings.Split(line, "\t")
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		if len(cols) < 4 {
			continue
		}
		r := run{status: cols[0], conclusion: cols[1], workflow: cols[2], branch: cols[3], age: cols[len(cols)-1]}
		if strings.EqualFold(r.conclusion, "failure") {
			failed++
		}
		runs = append(runs, r)
	}
	if len(runs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d runs (%d failed)", len(runs), failed)
	for _, r := range runs {
		fmt.Fprintf(&b, "\n%s %s %s %s %s", r.status, r.conclusion, r.workflow, r.branch, r.age)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}
