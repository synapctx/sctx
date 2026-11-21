package netinfo

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	hopRE  = regexp.MustCompile(`^\s*(\d+)\s+(.*)$`)
	starRE = regexp.MustCompile(`^(\*\s*)+$`)
)

// renderTraceroute folds runs of 2+ consecutive hops that answered nothing.
func renderTraceroute(argv, lines []string, aggressive bool) result {
	if !aggressive {
		return result{}
	}
	var out []string
	elided, hops := 0, 0
	for i := 0; i < len(lines); i++ {
		m := hopRE.FindStringSubmatch(lines[i])
		if m == nil || !starRE.MatchString(strings.TrimSpace(m[2])) {
			if m != nil {
				hops++
			}
			out = append(out, lines[i])
			continue
		}
		first, _ := strconv.Atoi(m[1])
		j := i
		for j+1 < len(lines) {
			n := hopRE.FindStringSubmatch(lines[j+1])
			if n == nil || !starRE.MatchString(strings.TrimSpace(n[2])) {
				break
			}
			j++
		}
		hops += j - i + 1
		if j == i {
			out = append(out, lines[i])
			continue
		}
		out = append(out, fmt.Sprintf("%d-%d  * (no reply) ×%d", first, first+(j-i), j-i+1))
		elided += j - i
		i = j
	}
	if hops == 0 || elided == 0 {
		return result{}
	}
	return result{out: out, elided: elided, ok: true}
}
