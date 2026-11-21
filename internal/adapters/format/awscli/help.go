package awscli

import (
	"regexp"
	"strings"
)

// overstrikeRE matches nroff overstrike: bold is "X\bX", underline "_\bX".
var overstrikeRE = regexp.MustCompile(`(?s).\x08(.)`)

// renderHelp strips overstrike and ANSI from an `aws ... help` man page and
// collapses runs of blank lines to one. Text is otherwise untouched.
func renderHelp(text string) string {
	if !strings.Contains(text, "\x08") && !ansiRE.MatchString(text) {
		return ""
	}
	text = ansiRE.ReplaceAllString(text, "")
	text = overstrikeRE.ReplaceAllString(text, "$1")
	var out []string
	blank := false
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		l = strings.TrimRight(l, " \r")
		if l == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
