package gh

import (
	"fmt"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveChecks retains actionable checks and replaces passing/skipped
// rows with explicit counted elisions. Native exit 1 means checks failed and
// is still a valid query result.
func aggressiveChecks(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	type check struct{ name, state, duration, url string }
	var actionable []check
	counts := make(map[string]int)
	total := 0

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(line, "\t") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 4 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		name := strings.TrimSpace(cols[0])
		state := strings.ToLower(strings.TrimSpace(cols[1]))
		if name == "" || state == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		total++
		counts[state]++
		if state != "pass" && state != "success" && state != "skipping" && state != "skipped" {
			actionable = append(actionable, check{name: name, state: state, duration: cols[2], url: cols[3]})
		}
	}
	if total == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d checks: %s", total, checkCountSummary(counts))
	for _, c := range actionable {
		fmt.Fprintf(&b, "\n%s\t%s\t%s\t%s", c.name, c.state, c.duration, c.url)
	}
	passing := counts["pass"] + counts["success"]
	skipped := counts["skipping"] + counts["skipped"]
	if passing > 0 {
		fmt.Fprintf(&b, "\n…+%d passing checks", passing)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "\n…+%d skipped checks", skipped)
	}
	body := b.String()
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d checks", total)}, nil
}

func checkCountSummary(counts map[string]int) string {
	order := []string{"fail", "failure", "pending", "queued", "in_progress", "pass", "success", "skipping", "skipped", "cancel"}
	var parts []string
	seen := make(map[string]bool)
	for _, state := range order {
		if n := counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, state))
			seen[state] = true
		}
	}
	var rest []string
	for state := range counts {
		if !seen[state] {
			rest = append(rest, state)
		}
	}
	sort.Strings(rest)
	for _, state := range rest {
		parts = append(parts, fmt.Sprintf("%d %s", counts[state], state))
	}
	return strings.Join(parts, ", ")
}
