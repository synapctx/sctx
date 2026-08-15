package gh

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type viewOptions struct {
	bodyCap     int
	repeatedKey string
	repeatedCap int
}

// aggressiveView parses gh's current `key:\tvalue` metadata, `--` delimiter,
// and Markdown body. Empty metadata and capped body/assets are always counted.
func aggressiveView(in format.Input, opts viewOptions) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	delim := -1
	for i, line := range lines {
		if line == "--" {
			delim = i
			break
		}
	}
	if delim < 1 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var meta []string
	emptyMeta, repeated := 0, 0
	for _, line := range lines[:delim] {
		key, value, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasSuffix(key, ":") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if strings.TrimSpace(value) == "" {
			emptyMeta++
			continue
		}
		if opts.repeatedKey != "" && strings.TrimSuffix(key, ":") == opts.repeatedKey {
			repeated++
			if repeated > opts.repeatedCap {
				continue
			}
		}
		meta = append(meta, line)
	}
	if emptyMeta > 0 {
		meta = append(meta, fmt.Sprintf("…+%d empty metadata fields", emptyMeta))
	}
	if repeated > opts.repeatedCap {
		meta = append(meta, fmt.Sprintf("…+%d more %ss", repeated-opts.repeatedCap, opts.repeatedKey))
	}

	body := lines[delim+1:]
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	shownBody := body
	if opts.bodyCap > 0 && len(shownBody) > opts.bodyCap {
		shownBody = shownBody[:opts.bodyCap]
	}
	out := append([]string{}, meta...)
	if len(shownBody) > 0 {
		out = append(out, "--")
		out = append(out, shownBody...)
	}
	if extra := len(body) - len(shownBody); extra > 0 {
		out = append(out, fmt.Sprintf("…+%d body lines", extra))
	}
	rendered := strings.Join(out, "\n")
	if len(rendered) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(rendered)}, nil
}
