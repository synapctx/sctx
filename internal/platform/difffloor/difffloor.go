// Package difffloor holds the fidelity floor for diff rendering. Agents derive
// edits from diff hunks, so compressing a small diff saves pennies while
// risking a wrong edit: a diff at or below the floor passes through verbatim.
package difffloor

import (
	"os"
	"strconv"
	"unicode/utf8"
)

// EnvVar names the environment variable that overrides the floor.
const EnvVar = "SCT__DIFF_FIDELITY_LINES"

// DefaultLines is the floor when EnvVar is unset or invalid.
const DefaultLines = 200

// Lines returns the configured floor: diffs of at most this many lines are
// never compacted. 0 disables the floor. An unset, non-numeric or negative
// value falls back to DefaultLines; configuration cannot break a command.
func Lines() int {
	v, ok := os.LookupEnv(EnvVar)
	if !ok {
		return DefaultLines
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return DefaultLines
	}
	return n
}

// Below reports whether a diff of the given line count is small enough to
// pass through untouched: at most Lines() lines.
func Below(lines int) bool {
	return lines <= Lines()
}

// Text reports whether raw is plain UTF-8 text with no NUL bytes. Binary and
// non-UTF-8 diffs are never rewritten.
func Text(raw []byte) bool {
	for _, b := range raw {
		if b == 0 {
			return false
		}
	}
	return utf8.Valid(raw)
}

// Stamp is the first line of a compacted diff, stating the loss.
func Stamp(rawLines, keptLines int) string {
	return "sctx: compacted diff " + strconv.Itoa(rawLines) + "→" + strconv.Itoa(keptLines) + " lines, context trimmed"
}
