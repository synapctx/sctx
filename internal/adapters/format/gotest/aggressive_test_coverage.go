package gotest

import "strings"

// coverageSuffix returns the "coverage: NN.N% of statements" suffix of l,
// if present, trimmed of surrounding whitespace. Used to retain -cover
// results that would otherwise be dropped when a line is collapsed to a
// count (e.g. an "ok" summary line).
func coverageSuffix(l string) (string, bool) {
	idx := strings.Index(l, "coverage:")
	if idx < 0 {
		return "", false
	}
	return strings.TrimSpace(l[idx:]), true
}
