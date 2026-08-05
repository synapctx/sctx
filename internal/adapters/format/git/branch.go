package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// branchCap is the number of non-current branches kept by `git branch`
// before eliding the rest.
const branchCap = 30

// aggressiveBranch caps `git branch`/`git branch -a`/`git branch -v` output
// (one branch name per line, current branch marked with a leading "*") to
// branchCap entries, always keeping the current branch regardless of its
// position in the list.
func aggressiveBranch(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lines) <= branchCap {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var current string
	var others []string
	for _, l := range lines {
		if strings.HasPrefix(l, "*") {
			current = l
			continue
		}
		others = append(others, l)
	}

	budget := branchCap
	if current != "" {
		budget--
	}
	shown := others
	extra := 0
	if len(others) > budget {
		extra = len(others) - budget
		shown = others[:budget]
	}

	var out []string
	if current != "" {
		out = append(out, current)
	}
	out = append(out, shown...)
	if extra > 0 {
		out = append(out, fmt.Sprintf("…+%d more branches", extra))
	}

	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d branches (%d shown)", len(lines), len(out)),
	}, nil
}

// nonEmptyLines drops blank lines from a split-line slice.
func nonEmptyLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}
