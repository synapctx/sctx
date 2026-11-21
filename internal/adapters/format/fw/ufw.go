package fw

import (
	"regexp"
	"strings"
)

var gapRE = regexp.MustCompile(`\s{2,}`)

// renderUFW squeezes the column padding of `ufw status [verbose|numbered]` to a
// double space, which keeps the column boundary visible while "ALLOW IN" and
// "22/tcp (v6)" keep their single spaces.
func renderUFW(args, lines []string) result {
	verb := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			verb = a
			break
		}
	}
	if verb != "status" {
		return result{}
	}
	var out []string
	rows := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			out = append(out, "")
			continue
		}
		s := gapRE.ReplaceAllString(strings.TrimSpace(l), "  ")
		if strings.Contains(s, "ALLOW") || strings.Contains(s, "DENY") || strings.Contains(s, "REJECT") || strings.Contains(s, "LIMIT") {
			rows++
		}
		out = append(out, s)
	}
	if rows == 0 {
		return result{}
	}
	return result{out: collapseBlank(out), ok: true}
}

func collapseBlank(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	return out
}
