package grep

import (
	"regexp"
	"strings"
)

// valueFlags are grep/rg options whose NEXT argv element is a value, so that
// element must not be mistaken for the search pattern.
var valueFlags = map[string]bool{
	"-A": true, "-B": true, "-C": true, "-m": true, "-d": true, "-D": true,
	"-g": true, "-t": true, "-T": true, "-f": true, "-j": true,
	"--after-context": true, "--before-context": true, "--context": true,
	"--max-count": true, "--glob": true, "--type": true, "--type-not": true,
	"--include": true, "--exclude": true, "--exclude-dir": true, "--file": true,
	"--directories": true, "--devices": true, "--label": true, "--threads": true,
	"--max-depth": true, "--maxdepth": true, "--iglob": true, "--ignore-file": true,
}

// patternRegexp derives a regexp for the search pattern from argv, used only
// to centre long-line truncation on the first match. It returns nil whenever
// the pattern cannot be identified or compiled with confidence (a -f pattern
// file, several -e patterns, a BRE-only syntax RE2 rejects), in which case
// callers keep the plain head-based truncation. A wrong guess is harmless: a
// regexp that does not match a line also falls back to the head window.
func patternRegexp(argv []string) *regexp.Regexp {
	var pats []string
	fixed, fold := false, false
	positional := ""
	havePositional := false
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			if !havePositional && i+1 < len(argv) {
				positional, havePositional = argv[i+1], true
			}
			i = len(argv)
		case a == "-e" || a == "--regexp":
			if i+1 < len(argv) {
				pats = append(pats, argv[i+1])
				i++
			}
		case strings.HasPrefix(a, "--regexp="):
			pats = append(pats, strings.TrimPrefix(a, "--regexp="))
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "--file="):
			return nil
		case a == "-F" || a == "--fixed-strings":
			fixed = true
		case a == "-i" || a == "--ignore-case":
			fold = true
		case valueFlags[a]:
			i++
		case strings.HasPrefix(a, "--"):
			// long flag with an inline or no value
		case strings.HasPrefix(a, "-") && len(a) > 1:
			// short-flag cluster: only the flags we track matter here.
			if strings.ContainsAny(a[1:], "Fi") {
				fixed = fixed || strings.Contains(a[1:], "F")
				fold = fold || strings.Contains(a[1:], "i")
			}
		default:
			if !havePositional {
				positional, havePositional = a, true
			}
		}
	}
	if len(pats) == 0 && havePositional {
		pats = []string{positional}
	}
	if len(pats) != 1 || pats[0] == "" {
		return nil
	}
	p := pats[0]
	if fixed {
		p = regexp.QuoteMeta(p)
	}
	if fold {
		p = "(?i)" + p
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil
	}
	return re
}

// truncateAround shortens s to at most max runes like truncate, but when re
// matches beyond the head window it centres the window on the first match so
// the part the user searched for stays visible. Cut ends are marked with "…".
func truncateAround(s string, max int, re *regexp.Regexp) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if re == nil {
		return truncate(s, max)
	}
	loc := re.FindStringIndex(s)
	if loc == nil || loc[1] == loc[0] {
		return truncate(s, max)
	}
	start := len([]rune(s[:loc[0]]))
	end := start + len([]rune(s[loc[0]:loc[1]]))
	if end <= max {
		return truncate(s, max)
	}
	mlen := end - start
	from := start
	if mlen < max {
		from = start - (max-mlen)/2
	}
	from = min(max0(from), len(r)-max)
	to := from + max
	out := string(r[from:to])
	if from > 0 {
		out = "…" + out
	}
	if to < len(r) {
		out += "…"
	}
	return out
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
