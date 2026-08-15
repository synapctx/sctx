package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

func aggressiveRunList(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var rows []string
	failed, active, completed := 0, 0, 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 9 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		status, conclusion := strings.ToLower(cols[0]), strings.ToLower(cols[1])
		if conclusion == "failure" {
			failed++
		}
		if status == "completed" {
			completed++
		} else {
			active++
		}
		name := cols[3]
		if cols[2] != cols[3] {
			name += ": " + cols[2]
		}
		state := conclusion
		if state == "" {
			state = status
		}
		rows = append(rows, fmt.Sprintf("%s %s [%s %s] id=%s %s", state, name, cols[4], cols[5], cols[6], cols[7]))
	}
	if len(rows) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	shown := rows
	if len(shown) > listCap {
		shown = shown[:listCap]
	}
	header := fmt.Sprintf("%d runs (%d failed, %d active); start timestamps omitted ×%d", len(rows), failed, active, len(rows))
	if completed > 0 {
		header += fmt.Sprintf("; completed status omitted ×%d", completed)
	}
	out := append([]string{header}, shown...)
	if extra := len(rows) - len(shown); extra > 0 {
		out = append(out, fmt.Sprintf("…+%d more runs", extra))
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d runs", len(rows))}, nil
}

func aggressiveRunView(in format.Input, args []string) (format.Rendered, error) {
	if hasArg(args, "--log", "--log-failed") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var out []string
	for i := 0; i < len(lines); {
		if strings.HasPrefix(lines[i], "  ✓ ") {
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], "  ✓ ") {
				j++
			}
			if j-i >= 4 {
				out = append(out, fmt.Sprintf("  …+%d successful steps", j-i))
				i = j
				continue
			}
		}
		out = append(out, lines[i])
		i++
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}

func hasArg(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}
