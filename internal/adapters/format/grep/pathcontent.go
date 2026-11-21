package grep

import (
	"os"
	"path"
	"strings"
)

// hasContextFlags reports whether argv requests context lines (-A/-B/-C,
// -NUM, --context and friends).
func hasContextFlags(argv []string) bool {
	for i, a := range argv {
		if i == 0 {
			continue
		}
		if a == "--" {
			return false
		}
		switch {
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a, "=")
			if name == "--context" || name == "--after-context" || name == "--before-context" {
				return true
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for _, c := range a[1:] {
				if c == 'A' || c == 'B' || c == 'C' || (c >= '0' && c <= '9') {
					return true
				}
				if c == 'e' || c == 'f' || c == 'm' || c == 'g' || c == 't' || c == 'T' {
					break // the rest of the group (or next arg) is a value
				}
			}
		}
	}
	return false
}

// maxStatPaths bounds the filesystem probing done to validate a path prefix.
const maxStatPaths = 500

// parsePathContent groups `path:content` lines (grep -r / rg without -n,
// the shape rg prints when stdout is not a terminal). The first colon is
// ambiguous with colons in a single file's content, so every distinct path
// token must name a file that exists; otherwise the output is not grouped
// (ok=false) and the caller declines.
func parsePathContent(lines []string, argv []string) ([]*fileGroup, int, bool) {
	if flagPresent(argv, 'c', "--count") || flagPresent(argv, 'Z', "--null") || flagPresent(argv, 'z', "--null-data") {
		return nil, 0, false
	}
	var order []string
	byPath := map[string]*fileGroup{}
	total := 0
	for _, ln := range lines {
		if ln == "" || ln == "--" {
			continue
		}
		p, content, ok := strings.Cut(ln, ":")
		if !ok || p == "" || strings.ContainsAny(p, " \t") {
			return nil, 0, false
		}
		g, seen := byPath[p]
		if !seen {
			if len(byPath) >= maxStatPaths {
				return nil, 0, false
			}
			if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
				return nil, 0, false
			}
			g = &fileGroup{path: p}
			byPath[p] = g
			order = append(order, p)
		}
		g.matches = append(g.matches, matchLine{content: content})
		total++
	}
	if total == 0 {
		return nil, 0, false
	}
	groups := make([]*fileGroup, 0, len(order))
	for _, p := range order {
		groups = append(groups, byPath[p])
	}
	return groups, total, true
}

// flagPresent reports whether argv carries the short flag letter (alone or
// bundled) or the long flag.
func flagPresent(argv []string, short rune, long string) bool {
	for i, a := range argv {
		if i == 0 {
			continue
		}
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "--") {
			if a == long {
				return true
			}
			continue
		}
		if strings.HasPrefix(a, "-") && strings.ContainsRune(a[1:], short) {
			return true
		}
	}
	return false
}

// machineOutput reports whether argv selects an output shape meant for a
// program rather than a reader (rg --json, NUL-separated records): neither
// tier may re-flow, truncate or cap such output, because doing so corrupts
// the records.
func machineOutput(argv []string) bool {
	if flagPresent(argv, 0, "--json") || flagPresent(argv, 'Z', "--null") || flagPresent(argv, 0, "--null-data") {
		return true
	}
	// rg spells --null -0; for grep a digit is a context count.
	return len(argv) > 0 && path.Base(argv[0]) == "rg" && flagPresent(argv, '0', "--null")
}
