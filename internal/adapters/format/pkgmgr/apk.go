package pkgmgr

import (
	"fmt"
	"regexp"
	"strings"
)

var apkNoise = rules(
	"fetch", `^fetch https?://`,
	"trigger", `^Executing \S+\.trigger$`,
)

var apkStep = regexp.MustCompile(`^\(\d+/\d+\) (Installing|Upgrading|Purging) (\S+) \((.+)\)$`)

func renderApk(a *acc, lines []string, aggressive bool) {
	type group struct {
		verb  string
		items []string
	}
	var groups []*group
	byVerb := map[string]*group{}
	slot := map[string]int{}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if cat, ok := apkNoise.match(l); ok {
			a.drop(cat, l)
			continue
		}
		if m := apkStep.FindStringSubmatch(l); m != nil && aggressive {
			g := byVerb[m[1]]
			if g == nil {
				g = &group{verb: m[1]}
				byVerb[m[1]] = g
				groups = append(groups, g)
				slot[m[1]] = len(a.out)
				a.keep("") // placeholder, filled below to keep source order
			}
			ver := strings.Replace(m[3], " -> ", "→", 1)
			g.items = append(g.items, m[2]+" "+ver)
			a.last = l
			continue
		}
		a.keep(l)
	}
	for _, g := range groups {
		a.out[slot[g.verb]] = fmt.Sprintf("%s (%d): %s", g.verb, len(g.items), strings.Join(g.items, ", "))
	}
}
