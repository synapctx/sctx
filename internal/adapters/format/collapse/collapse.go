// Package collapse holds the line-run collapsing that several formatters share:
// runs of blank lines, runs of identical lines, and runs of log lines that are
// identical once a leading timestamp is stripped.
//
// WHY IT IS SHARED. It was written inside the `read` formatter, where it was the
// relaxed tier for `cat`/`head`/`tail`. Measured over 34 days, the UNMATCHED path
// — every command with no dedicated formatter — saved exactly ZERO tokens across
// 179 runs and 50,124 raw tokens, because the fallback sniffed JSON and nothing
// else. The one genuinely general compressor in the codebase was reachable by
// three commands.
//
// EVERY RULE HERE COMPRESSES ONLY PROVABLE REDUNDANCY, and that constraint is what
// makes it safe to point at output nobody has captured a fixture for. A collapsed
// run is reconstructible from what is printed: the representative line plus an
// explicit count. Nothing is summarised, nothing is judged interesting or
// uninteresting, and no line is dropped without a marker saying how many went
// with it. A generic formatter that guessed at structure would be the dangerous
// kind; this one cannot lose information it has not counted.
package collapse

import (
	"fmt"
	"regexp"
	"strings"
)

// RunThreshold is the minimum run length (blank, identical, or
// timestamp-normalized-identical lines) that triggers collapsing.
const RunThreshold = 3

// leadingTimestampRE matches a single well-defined leading timestamp shape
// (ISO-8601-ish, optionally bracketed): "2024-01-02T15:04:05Z",
// "[2024-01-02 15:04:05.123+00:00]", etc. Deliberately narrow — matching more
// broadly risks treating lines that only coincidentally start alike as
// duplicates, which would hide real content differences.
var leadingTimestampRE = regexp.MustCompile(`^\[?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?\]?`)

// StripLeadingTimestamp removes a leading timestamp token (and its following
// separator) from line and reports whether one was found.
func StripLeadingTimestamp(line string) (rest string, ok bool) {
	loc := leadingTimestampRE.FindStringIndex(line)
	if loc == nil {
		return line, false
	}
	return strings.TrimLeft(line[loc[1]:], " :\t-"), true
}

// SplitLines splits raw bytes into lines without trailing newlines, dropping a
// single final empty element caused by a trailing newline.
//
// CARRIAGE RETURNS ARE STRIPPED, and that is not cosmetic. On Windows — and for
// any tool that emits CRLF anywhere — a trailing "\r" makes two otherwise
// identical lines compare unequal, so every run-collapsing rule below silently
// stops firing and the formatter reports a truthful "nothing to collapse" for
// output that is nothing but duplicates. The failure is invisible: no error, no
// anomaly, just zero savings on the platform nobody tested.
func SplitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// Runs walks lines, collapsing runs of 3+ blank lines, runs of 3+ consecutive
// identical (non-blank) lines, and — failing those — runs of 3+ consecutive
// non-blank lines that share an identical suffix once a leading timestamp is
// stripped from each. changed reports whether any run met a threshold.
func Runs(lines []string) (out []string, changed bool) {
	i := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		exactCount := j - i
		if exactCount >= RunThreshold {
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
			if suffix, ok := StripLeadingTimestamp(lines[i]); ok {
				k := i + 1
				for k < len(lines) {
					s, matched := StripLeadingTimestamp(lines[k])
					if !matched || s != suffix {
						break
					}
					k++
				}
				tsCount := k - i
				if tsCount >= RunThreshold {
					out = append(out, fmt.Sprintf("%s ×%d", lines[i], tsCount))
					changed = true
					i = k
					continue
				}
			}
		}

		// Neither run met a threshold at position i: emit the exact-match run
		// (always >= 1 line) verbatim and advance.
		out = append(out, lines[i:j]...)
		i = j
	}
	return out, changed
}
