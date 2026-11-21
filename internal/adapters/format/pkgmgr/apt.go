package pkgmgr

import (
	"fmt"
	"regexp"
	"strings"
)

type noiseRule struct {
	cat string
	re  *regexp.Regexp
}

type ruleSet []noiseRule

func rules(pairs ...string) ruleSet {
	rs := make(ruleSet, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		rs = append(rs, noiseRule{pairs[i], regexp.MustCompile(pairs[i+1])})
	}
	return rs
}

func (rs ruleSet) match(l string) (string, bool) {
	for _, r := range rs {
		if r.re.MatchString(l) {
			return r.cat, true
		}
	}
	return "", false
}

// aptNoise lists the per-package progress lines of apt/apt-get/dpkg.
var aptNoise = rules(
	"Reading", `^(Reading package lists|Building dependency tree|Reading state information|Reading database)`,
	"Get", `^Get:\d+ `,
	"Hit", `^Hit:\d+ `,
	"Ign", `^Ign:\d+ `,
	"db", `^\(Reading database`,
	"Selecting", `^Selecting previously (un)?selected package`,
	"Preparing", `^Preparing to (unpack|replace)`,
	"Unpacking", `^Unpacking `,
	"Setting up", `^Setting up `,
	"Removing", `^Removing `,
	"triggers", `^Processing triggers for`,
	"debconf", `^(debconf|dpkg-preconfigure): `,
	"Conf", `^Conf `,
	"templates", `^(Extracting templates from packages|Preconfiguring packages)`,
	"needrestart", `^(Scanning (processes|candidates|linux images)|Running kernel seems to be up-to-date|No services need to be restarted|No containers need to be restarted|No user sessions are running outdated|No VM guests are running outdated)`,
	"apt-cli-warning", `^WARNING: apt does not have a stable CLI`,
)

// aptBlocks maps a block header to a short label; a block is the header line
// followed by two-space-indented package names.
var aptBlocks = []struct{ prefix, label string }{
	{"The following NEW packages will be installed:", "NEW"},
	{"The following packages will be upgraded:", "UPGRADE"},
	{"The following packages will be REMOVED:", "REMOVE"},
	{"The following packages will be DOWNGRADED:", "DOWNGRADE"},
	{"The following packages have been kept back:", "KEPT BACK"},
	{"The following packages were automatically installed and are no longer required:", "AUTOREMOVABLE"},
	{"The following held packages will be changed:", "HELD CHANGED"},
	{"The following packages will be marked as automatically installed:", ""},
}

func renderApt(a *acc, lines []string, aggressive bool) {
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if cat, ok := aptNoise.match(l); ok {
			a.drop(cat, l)
			continue
		}
		if aggressive {
			if hdr, ok := aptBlockHeader(l); ok {
				names, next := blockBody(lines, i+1)
				i = next - 1
				switch hdr {
				case "additional":
					a.drop("additional-deps", "")
				case "suggested":
					a.drop("suggested", "")
				default:
					a.keep(fmt.Sprintf("%s (%d): %s", hdr, len(names), strings.Join(names, " ")))
				}
				continue
			}
		}
		a.keep(l)
	}
}

// aptBlockHeader recognises the package-list headers. "additional" and
// "suggested" blocks are redundant with the NEW list and with nothing the agent
// acts on, so they are returned as sentinel labels and counted.
func aptBlockHeader(l string) (string, bool) {
	for _, b := range aptBlocks {
		if strings.HasPrefix(l, b.prefix) && b.label != "" {
			return b.label, true
		}
	}
	switch {
	case strings.HasPrefix(l, "The following additional packages will be installed:"):
		return "additional", true
	case l == "Suggested packages:" || l == "Recommended packages:":
		return "suggested", true
	}
	return "", false
}

// blockBody reads the indented continuation lines starting at i and returns the
// whitespace-split entries and the index of the first line after the block.
func blockBody(lines []string, i int) (names []string, next int) {
	for i < len(lines) && strings.HasPrefix(lines[i], "  ") {
		names = append(names, strings.Fields(lines[i])...)
		i++
	}
	return names, i
}

var aptListRE = regexp.MustCompile(`^([^/\s]+)/(\S+) (\S+) (\S+)(?: \[(.*)\])?$`)
var aptFromRE = regexp.MustCompile(`^upgradable from: (.+)$`)

// renderAptList compacts `apt list` rows to `name version` (and the old version
// for --upgradable). Suite and architecture are folded into a header when
// uniform and kept per row when not; security suites are flagged.
func renderAptList(a *acc, lines []string, aggressive bool) {
	type row struct{ name, suites, ver, arch, flags, from string }
	var rows []row
	var other []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if cat, ok := aptNoise.match(l); ok {
			a.drop(cat, l)
			continue
		}
		if l == "Listing..." || l == "Listing... Done" {
			a.drop("Listing", l)
			continue
		}
		m := aptListRE.FindStringSubmatch(l)
		if m == nil || !aggressive {
			other = append(other, l)
			continue
		}
		r := row{name: m[1], suites: m[2], ver: m[3], arch: m[4], flags: m[5]}
		if fm := aptFromRE.FindStringSubmatch(strings.TrimPrefix(r.flags, "installed,")); fm != nil {
			r.from, r.flags = fm[1], ""
		}
		rows = append(rows, r)
	}
	for _, l := range other {
		a.keep(l)
	}
	if len(rows) == 0 {
		return
	}
	arch := map[string]int{}
	allInstalled := true
	for _, r := range rows {
		arch[r.arch]++
		if r.from != "" || !strings.HasPrefix(r.flags, "installed") {
			allInstalled = false
		}
	}
	main := ""
	for k, n := range arch {
		if main == "" || n > arch[main] {
			main = k
		}
	}
	hdr := fmt.Sprintf("%d packages (arch %s", len(rows), main)
	if allInstalled {
		hdr += ", all installed"
	}
	a.keep(hdr + "; suite dropped, [sec] = security suite)")
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(r.name + " ")
		if r.from != "" {
			b.WriteString(r.from + "→")
		}
		b.WriteString(r.ver)
		if r.arch != main {
			b.WriteString(" (" + r.arch + ")")
		}
		flags := r.flags
		if allInstalled {
			flags = strings.TrimPrefix(strings.TrimPrefix(flags, "installed"), ",")
		}
		if flags != "" {
			b.WriteString(" [" + flags + "]")
		}
		if r.from != "" && strings.Contains(r.suites, "security") {
			b.WriteString(" [sec]")
		}
		a.keep(b.String())
	}
}
