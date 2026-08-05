package fs

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// readStdout reads and returns in.Stdout as a string. A nil reader yields
// an empty string.
func readStdout(in format.Input) (string, error) {
	if in.Stdout == nil {
		return "", nil
	}
	b, err := io.ReadAll(in.Stdout)
	if err != nil {
		return "", fmt.Errorf("reading stdout: %w", err)
	}
	return string(b), nil
}

// splitLines splits raw into lines, dropping a single trailing newline so
// callers don't see a spurious empty final line.
func splitLines(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(raw, "\n"), "\n")
}

// nonEmptyLines filters out blank lines (after trimming whitespace).
func nonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// dedupeConsecutive collapses runs of identical consecutive lines to a
// single occurrence.
func dedupeConsecutive(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[0])
	for _, l := range lines[1:] {
		if l == out[len(out)-1] {
			continue
		}
		out = append(out, l)
	}
	return out
}

const relaxedLineCap = 250

// relaxedRender implements the shared TierRelaxed behavior for all three fs
// formatters: dedupe identical consecutive lines, cap the total at
// relaxedLineCap lines with an explicit "…+N more lines" marker, and leave
// stderr untouched (no folding).
func relaxedRender(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := dedupeConsecutive(splitLines(raw))
	if len(lines) > relaxedLineCap {
		extra := len(lines) - relaxedLineCap
		lines = append(lines[:relaxedLineCap], fmt.Sprintf("…+%d more lines", extra))
	}

	body := strings.Join(lines, "\n")
	if body == "" || len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}

// shrunk reports whether body is a non-empty, strictly smaller rendering of
// raw. Formatters use this as the final safety gate before returning a
// tier's result.
func shrunk(raw, body string) bool {
	return body != "" && len(body) < len(raw)
}
