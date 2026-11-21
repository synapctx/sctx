package pkgmgr

import (
	"fmt"
	"regexp"
	"strings"
)

// rpmNoise covers dnf5, dnf4 and yum (dnf4 under the yum name) progress.
var rpmNoise = rules(
	"Total", `^(\[\d+/\d+\] )?Total\s+(\d+% \| )?[\d.]+ \S+/s \|`,
	"steps", `^\[\d+/\d+\] (Verify package files|Prepare transaction|Installing|Upgrading|Removing|Erasing|Reinstalling|Downgrading|Obsoleting|Cleanup|Running)`,
	"download", `^\[\d+/\d+\] \S`,
	"download", `^\(\d+/\d+\): \S`,
	"repo-load", `^Updating and loading repositories:$`,
	"repo-load", `^Repositories loaded\.$`,
	"repo-load", `^\s?\S.*\s+\d+% \|\s*[\d.]+\s*\S+/s \|\s*[\d.]+\s*\S+ \|\s*\d+m\d+s$`,
	"metadata", `^Last metadata expiration check:`,
	"repo-load", `^\S.*\s+[\d.]+ [kMG]?B/s \| [\d.]+ [kMG]?B\s+\d\d:\d\d$`,
	"Downloading", `^Downloading Packages:$`,
	"txn-check", `^(Running transaction( check| test)?|Transaction (check|test) succeeded\.)$`,
	"steps", `^\s+(Preparing|Running scriptlet|Installing|Verifying|Erasing|Upgrading|Cleanup|Reinstalling|Downgrading|Obsoleting)\s*:.*\s\d+/\d+$`,
)

var (
	rpmRuleRE   = regexp.MustCompile(`^[=-]{20,}$`)
	rpmDoneHdr  = regexp.MustCompile(`^(Installed|Removed|Upgraded|Downgraded|Reinstalled|Replaced):$`)
	rpmTableHdr = regexp.MustCompile(`^\s*Package\s+(Arch|Architecture)\b`)
)

func renderRPM(a *acc, lines []string, aggressive bool, op string) {
	inTable, sawTable := false, false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			inTable = false
			continue
		}
		if rpmRuleRE.MatchString(l) {
			continue // a rule line is formatting, not content
		}
		if cat, ok := rpmNoise.match(l); ok {
			a.drop(cat, l)
			continue
		}
		if aggressive {
			if rpmDoneHdr.MatchString(l) {
				names, next := blockBody(lines, i+1)
				i = next - 1
				if sawTable {
					a.drop("done-list", "") // repeats the transaction table above
					continue
				}
				a.keep(fmt.Sprintf("%s (%d): %s", strings.TrimSuffix(l, ":"), len(names), strings.Join(names, " ")))
				continue
			}
			switch {
			case rpmTableHdr.MatchString(l):
				inTable, sawTable = true, true
				a.keep(squeeze(l))
				continue
			case strings.HasPrefix(l, "Transaction Summary"):
				inTable = true // the summary rows are padded the same way, until the blank line
			case inTable, op == "check-update":
				a.keep(squeeze(l))
				continue
			}
		}
		a.keep(l)
	}
}
