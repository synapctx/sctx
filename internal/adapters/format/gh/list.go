package gh

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type listKind int

const (
	listPullRequests listKind = iota
	listIssues
	listRepositories
	listReleases
	listCap = 30
)

func aggressiveList(in format.Input, kind listKind) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var rows []string
	states := make(map[string]int)
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		row, state, ok := formatListRow(kind, cols)
		if !ok {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		rows = append(rows, row)
		if state != "" {
			states[strings.ToLower(state)]++
		}
	}
	if len(rows) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	shown := rows
	if len(shown) > listCap {
		shown = shown[:listCap]
	}
	header := fmt.Sprintf("%d %s", len(rows), listKindName(kind))
	if summary := stateSummary(states); summary != "" {
		header += " (" + summary + ")"
	}
	if kind != listReleases {
		header += fmt.Sprintf("; updated timestamps omitted ×%d", len(rows))
	}
	out := append([]string{header}, shown...)
	if extra := len(rows) - len(shown); extra > 0 {
		out = append(out, fmt.Sprintf("…+%d more rows", extra))
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d %s", len(rows), listKindName(kind))}, nil
}

func formatListRow(kind listKind, cols []string) (row, state string, ok bool) {
	switch kind {
	case listPullRequests:
		if len(cols) != 5 || !isNumber(cols[0]) {
			return "", "", false
		}
		return fmt.Sprintf("#%s %s %s [%s]", cols[0], cols[3], cols[1], cols[2]), cols[3], true
	case listIssues:
		if len(cols) != 5 || !isNumber(cols[0]) {
			return "", "", false
		}
		labels := ""
		if cols[3] != "" {
			labels = " [" + cols[3] + "]"
		}
		return fmt.Sprintf("#%s %s %s%s", cols[0], cols[1], cols[2], labels), cols[1], true
	case listRepositories:
		if len(cols) < 3 || cols[0] == "" {
			return "", "", false
		}
		// Native columns: name, description, visibility, updatedAt.
		return fmt.Sprintf("%s %s %s", cols[0], cols[2], cols[1]), cols[2], true
	case listReleases:
		if len(cols) != 4 || cols[0] == "" || cols[2] == "" {
			return "", "", false
		}
		label := cols[1]
		if label != "" {
			label += " "
		}
		return fmt.Sprintf("%s %s%s %s", cols[2], label, cols[0], cols[3]), cols[1], true
	default:
		return "", "", false
	}
}

func isNumber(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

func listKindName(kind listKind) string {
	switch kind {
	case listPullRequests:
		return "pull requests"
	case listIssues:
		return "issues"
	case listRepositories:
		return "repositories"
	case listReleases:
		return "releases"
	default:
		return "rows"
	}
}

func stateSummary(states map[string]int) string {
	order := []string{"failure", "open", "draft", "private", "public", "internal", "latest", "prerelease", "release"}
	var parts []string
	seen := make(map[string]bool)
	for _, state := range order {
		if n := states[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, state))
			seen[state] = true
		}
	}
	var rest []string
	for state := range states {
		if !seen[state] {
			rest = append(rest, state)
		}
	}
	sort.Strings(rest)
	for _, state := range rest {
		parts = append(parts, fmt.Sprintf("%d %s", states[state], state))
	}
	return strings.Join(parts, ", ")
}
