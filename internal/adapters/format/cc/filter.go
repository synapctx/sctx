package cc

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	// headerRE matches the first line of a clang/gcc diagnostic:
	// "path:line:col: error: message". The column is optional (gcc prints
	// "path:line: error:" for some preprocessor diagnostics).
	headerRE = regexp.MustCompile(`^\S.*?:\d+:(?:\d+:)? (fatal error|error|warning|note|remark): `)
	// gutterRE matches a source-excerpt line of the gutter style both clang and
	// modern gcc print: "    6 |     code", "      |     ^~~~", "      |  ;".
	// GNU gcc also prints "  +++ |+#include <stdlib.h>" for its fix-it hints.
	gutterRE = regexp.MustCompile(`^(?:\s*\d*\s*\|(?: |$)|\s*\+\+\+ \|\+)`)
	// flagRE pulls warning flags out of the trailing "[-Wfoo]" / "[-Werror,-Wfoo]"
	// (clang) and "[-Wformat=]" / "[-Werror=unused-variable]" (gcc).
	flagRE = regexp.MustCompile(`\[((?:-W[A-Za-z0-9+_=-]+(?:,|))+)\]$`)
	ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]|\x1b\\]8;;[^\x07\x1b]*(?:\x07|\x1b\\\\)")
)

// StripANSI removes SGR colour sequences and OSC-8 hyperlinks.
func StripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// Stats reports what Filter saw and dropped.
type Stats struct {
	Errors   int
	Warnings int
	// Dropped counts source-excerpt and caret lines removed.
	Dropped int
	// Deduped counts diagnostic lines folded into an earlier identical one.
	Deduped int
	Flags   map[string]int
}

// Filter drops the source excerpt and caret lines that follow each clang/gcc
// diagnostic header and, when dedupe is set, folds a repeated identical
// diagnostic line into its first occurrence as "line ×N". Every other line
// (including "In file included from", linker errors and summaries) is kept
// verbatim, so no error or warning message is ever lost. The caller owns the
// elision marker: see Footer.
func Filter(lines []string, dedupe bool) ([]string, Stats) {
	st := Stats{Flags: map[string]int{}}
	out := make([]string, 0, len(lines))
	seen := map[string]int{} // diagnostic line -> index in out
	counts := map[int]int{}
	inDiag := false
	for _, raw := range lines {
		line := StripANSI(raw)
		if m := headerRE.FindStringSubmatch(line); m != nil {
			inDiag = true
			switch m[1] {
			case "error", "fatal error":
				st.Errors++
			case "warning":
				st.Warnings++
				if f := warningFlag(line); f != "" {
					st.Flags[f]++
				}
			}
			if dedupe {
				if idx, ok := seen[line]; ok {
					counts[idx]++
					st.Deduped++
					continue
				}
				seen[line] = len(out)
			}
			out = append(out, line)
			continue
		}
		if inDiag && gutterRE.MatchString(line) {
			st.Dropped++
			continue
		}
		if inDiag && strings.TrimSpace(line) == "" {
			// A blank line between a diagnostic and its excerpt or successor is
			// layout only (swift prints one after every block).
			st.Dropped++
			continue
		}
		inDiag = false
		out = append(out, line)
	}
	for idx, n := range counts {
		out[idx] = fmt.Sprintf("%s ×%d", out[idx], n+1)
	}
	return out, st
}

func warningFlag(line string) string {
	m := flagRE.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	for _, f := range strings.Split(m[1], ",") {
		if f != "" && f != "-Werror" {
			return strings.Replace(f, "-Werror=", "-W", 1)
		}
	}
	return ""
}

// Footer renders the explicit elision markers for st, one line each. The
// warning-flag histogram is only worth its tokens from three warnings up.
func Footer(st Stats, histogram bool) []string {
	var out []string
	if st.Dropped > 0 {
		out = append(out, fmt.Sprintf("…+%d source/caret lines", st.Dropped))
	}
	if histogram && st.Warnings >= 3 && len(st.Flags) > 0 {
		type kv struct {
			k string
			n int
		}
		var kvs []kv
		for k, n := range st.Flags {
			kvs = append(kvs, kv{k, n})
		}
		sort.Slice(kvs, func(i, j int) bool {
			if kvs[i].n != kvs[j].n {
				return kvs[i].n > kvs[j].n
			}
			return kvs[i].k < kvs[j].k
		})
		parts := make([]string, len(kvs))
		for i, e := range kvs {
			parts[i] = fmt.Sprintf("%s ×%d", e.k, e.n)
		}
		out = append(out, "warnings by flag: "+strings.Join(parts, ", "))
	}
	return out
}
