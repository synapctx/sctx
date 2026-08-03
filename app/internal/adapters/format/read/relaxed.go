package read

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// relaxedRunThreshold is the minimum run length (blank, identical, or
// timestamp-normalized-identical lines) that triggers collapsing.
const relaxedRunThreshold = 3

// leadingTimestampRE matches a single well-defined leading timestamp shape
// (ISO-8601-ish, optionally bracketed): "2024-01-02T15:04:05Z",
// "[2024-01-02 15:04:05.123+00:00]", etc. Deliberately narrow — matching
// more broadly risks treating lines that only coincidentally start alike as
// duplicates, which would hide real content differences.
var leadingTimestampRE = regexp.MustCompile(`^\[?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?\]?`)

// stripLeadingTimestamp removes a leading timestamp token (and its
// following separator) from line and reports whether one was found.
func stripLeadingTimestamp(line string) (rest string, ok bool) {
	loc := leadingTimestampRE.FindStringIndex(line)
	if loc == nil {
		return line, false
	}
	return strings.TrimLeft(line[loc[1]:], " :\t-"), true
}

// Relaxed collapses runs of 3+ blank lines to one blank plus an explicit
// "…+N blank" marker, runs of 3+ consecutive identical lines to one line
// plus a "×N" marker, and runs of 3+ consecutive log lines that are
// identical once a leading timestamp is stripped to one representative line
// (with its own timestamp) plus a "×N" marker. If no run met a threshold,
// the tier is inapplicable so the chain tries the next tier instead of
// re-emitting the input unchanged.
func (f *formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("read: reading stdout: %w", err)
	}
	if len(raw) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := splitLines(raw)
	out, changed := collapseRuns(lines)
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	body := strings.Join(out, "\n")
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}

// splitLines splits raw bytes into lines without trailing newlines, dropping
// a single final empty element caused by a trailing newline.
func splitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// collapseRuns walks lines, collapsing runs of 3+ blank lines, runs of 3+
// consecutive identical (non-blank) lines, and — failing those — runs of 3+
// consecutive non-blank lines that share an identical suffix once a leading
// timestamp is stripped from each. changed reports whether any run met a
// threshold.
func collapseRuns(lines []string) (out []string, changed bool) {
	i := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		exactCount := j - i
		if exactCount >= relaxedRunThreshold {
			if lines[i] == "" {
				out = append(out, "", fmt.Sprintf("…+%d blank", exactCount-1))
			} else {
				out = append(out, fmt.Sprintf("%s ×%d", lines[i], exactCount))
			}
			changed = true
			i = j
			continue
		}

		if lines[i] != "" {
			if suffix, ok := stripLeadingTimestamp(lines[i]); ok {
				k := i + 1
				for k < len(lines) {
					s, matched := stripLeadingTimestamp(lines[k])
					if !matched || s != suffix {
						break
					}
					k++
				}
				tsCount := k - i
				if tsCount >= relaxedRunThreshold {
					out = append(out, fmt.Sprintf("%s ×%d", lines[i], tsCount))
					changed = true
					i = k
					continue
				}
			}
		}

		// Neither run met a threshold at position i: emit the exact-match
		// run (always >= 1 line) verbatim and advance.
		out = append(out, lines[i:j]...)
		i = j
	}
	return out, changed
}
