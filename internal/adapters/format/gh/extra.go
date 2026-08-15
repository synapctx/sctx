package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

func aggressiveSearch(in format.Input, kind string) (format.Rendered, error) {
	switch kind {
	case "prs", "issues":
		return aggressiveSearchIssues(in, kind)
	case "repos":
		return aggressiveList(in, listRepositories)
	case "commits":
		return aggressiveSearchCommits(in)
	case "code":
		return aggressiveLineCap(in, 100, "code result lines")
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

func aggressiveSearchIssues(in format.Input, kind string) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	states := make(map[string]int)
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 6 || cols[0] == "" || !isNumber(cols[1]) || cols[2] == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		state := strings.ToLower(cols[2])
		states[state]++
		labels := ""
		if cols[4] != "" {
			labels = " [" + cols[4] + "]"
		}
		rows = append(rows, fmt.Sprintf("%s#%s %s %s%s", cols[0], cols[1], state, cols[3], labels))
	}
	return renderRows(raw, rows, fmt.Sprintf("%d search %s (%s); updated timestamps omitted ×%d", len(rows), kind, stateSummary(states), len(rows)), kind)
}

func aggressiveSearchCommits(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rows := make([]string, 0, len(lines))
	shortened := 0
	for _, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 5 || cols[0] == "" || len(cols[1]) < 7 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		sha := cols[1]
		if len(sha) > 12 {
			sha = sha[:12]
			shortened++
		}
		rows = append(rows, fmt.Sprintf("%s %s %s — %s", cols[0], sha, cols[2], cols[3]))
	}
	header := fmt.Sprintf("%d search commits; timestamps omitted ×%d", len(rows), len(rows))
	if shortened > 0 {
		header += fmt.Sprintf("; commit SHAs shortened to 12 chars ×%d", shortened)
	}
	return renderRows(raw, rows, header, "commits")
}

func aggressiveWorkflowList(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rows := make([]string, 0, len(lines))
	states := make(map[string]int)
	for _, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 3 || cols[0] == "" || cols[1] == "" || !isNumber(cols[2]) {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		states[strings.ToLower(cols[1])]++
		rows = append(rows, fmt.Sprintf("%s %s", cols[2], cols[0]))
	}
	return renderRows(raw, rows, fmt.Sprintf("%d workflows (%s); states omitted ×%d", len(rows), stateSummary(states), len(rows)), "workflows")
}

func aggressiveCacheList(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 5 || !isNumber(cols[0]) || cols[1] == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		rows = append(rows, fmt.Sprintf("%s %s %s", cols[0], cols[2], cols[1]))
	}
	return renderRows(raw, rows, fmt.Sprintf("%d caches; created/accessed timestamps omitted ×%d", len(rows), len(rows)*2), "caches")
}

func aggressiveGistList(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 5 || cols[0] == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		rows = append(rows, strings.Join(cols[:4], " "))
	}
	return renderRows(raw, rows, fmt.Sprintf("%d gists; updated timestamps omitted ×%d", len(rows), len(rows)), "gists")
}

func aggressiveGistView(in format.Input, args []string) (format.Rendered, error) {
	if hasArg(args, "--raw", "-r", "--allow-escape-sequences") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if hasArg(args, "--files") {
		return aggressiveLineCap(in, 100, "gist files")
	}
	return aggressiveLineCap(in, 200, "gist lines")
}

func aggressiveTSVCap(in format.Input, cap int, noun string) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) <= cap {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	width := -1
	for _, line := range lines {
		cols := strings.Count(line, "\t") + 1
		if cols < 2 || (width >= 0 && cols != width) {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		width = cols
	}
	return cappedLines(raw, lines, cap, noun)
}

func aggressiveLineCap(in format.Input, cap int, noun string) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) <= cap {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return cappedLines(raw, lines, cap, noun)
}

func cappedLines(raw []byte, lines []string, cap int, noun string) (format.Rendered, error) {
	body := strings.Join(lines[:cap], "\n") + fmt.Sprintf("\n…+%d more %s", len(lines)-cap, noun)
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d %s", len(lines), noun)}, nil
}

func renderRows(raw []byte, rows []string, header, noun string) (format.Rendered, error) {
	shown := rows
	if len(shown) > listCap {
		shown = shown[:listCap]
	}
	out := append([]string{header}, shown...)
	if extra := len(rows) - len(shown); extra > 0 {
		out = append(out, fmt.Sprintf("…+%d more %s", extra, noun))
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d %s", len(rows), noun)}, nil
}
