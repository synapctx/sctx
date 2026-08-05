package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxBodyLines is the truncation width applied to `pr view`/`issue view`
// bodies before an elision marker is emitted.
const maxBodyLines = 25

// aggressiveView renders `gh pr view` / `gh issue view` output, keeping the
// title, state/author/labels metadata, and body truncated to maxBodyLines
// with an explicit "...+N lines" marker. Any comment thread section is
// dropped in favor of an explicit "...+N comments" marker.
func aggressiveView(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	title := lines[0]

	var meta []string
	i := 1
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			i++
			break
		}
		meta = append(meta, trimmed)
	}

	var body []string
	var comments []string
	inComments := false
	footer := ""

	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Comments":
			inComments = true
			continue
		case strings.HasPrefix(trimmed, "----"):
			continue
		case strings.HasPrefix(trimmed, "View this"):
			footer = line
			continue
		}
		if inComments {
			if trimmed != "" {
				comments = append(comments, line)
			}
			continue
		}
		body = append(body, line)
	}

	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}

	var bodyOut []string
	if len(body) > maxBodyLines {
		bodyOut = append(bodyOut, body[:maxBodyLines]...)
		bodyOut = append(bodyOut, fmt.Sprintf("…+%d lines", len(body)-maxBodyLines))
	} else {
		bodyOut = body
	}

	var b strings.Builder
	b.WriteString(title)
	for _, m := range meta {
		b.WriteByte('\n')
		b.WriteString(m)
	}
	if len(bodyOut) > 0 {
		b.WriteString("\n\n")
		b.WriteString(strings.Join(bodyOut, "\n"))
	}
	if len(comments) > 0 {
		fmt.Fprintf(&b, "\n\n…+%d comments", len(comments))
	}
	if footer != "" {
		b.WriteByte('\n')
		b.WriteString(footer)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}
