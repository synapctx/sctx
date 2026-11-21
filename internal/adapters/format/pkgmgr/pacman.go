package pkgmgr

import (
	"regexp"
	"strings"
)

var pacmanNoise = rules(
	"downloading", `^ \S+ downloading\.\.\.$`,
	"checks", `^(checking|loading|looking for|resolving) .*\.\.\.$`,
	"steps", `^(installing|upgrading|removing|reinstalling|downgrading) \S+\.\.\.$`,
	"phases", `^:: (Retrieving packages|Processing package changes|Running post-transaction hooks)\.\.\.$`,
	"hooks", `^\(\s*\d+/\d+\) `,
	"hooks", `^  Skipped: `,
	"hooks", `^(Generating locales\.\.\.|Generation complete\.)$`,
	"prompt", `^:: (Proceed with installation|Do you want to remove these packages)\?`,
)

var pacmanTableHdr = regexp.MustCompile(`^Package \(\d+\)`)

func renderPacman(a *acc, lines []string, aggressive bool) {
	inTable := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue // the table continues across its blank separator line
		}
		if cat, ok := pacmanNoise.match(l); ok {
			a.drop(cat, l)
			continue
		}
		if strings.HasPrefix(l, "Optional dependencies for ") {
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], "    ") {
				j++
			}
			a.drop("optional-deps", "")
			i = j - 1
			continue
		}
		if aggressive {
			switch {
			case pacmanTableHdr.MatchString(l):
				inTable = true
				a.keep(squeeze(l))
				continue
			case inTable, strings.HasPrefix(l, "Total "), strings.HasPrefix(l, "Net "):
				if strings.HasPrefix(l, "Total ") {
					inTable = false
				}
				a.keep(squeeze(l))
				continue
			}
		}
		a.keep(l)
	}
}
