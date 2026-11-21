package netinfo

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	ifaceHeadRE = regexp.MustCompile(`^(\d+): (\S+?): <([^>]*)> mtu (\d+)(.*)$`)
	stateRE     = regexp.MustCompile(`\bstate (\S+)`)
	masterRE    = regexp.MustCompile(`\bmaster (\S+)`)
	linkRE      = regexp.MustCompile(`^\s+link/(\S+)(?: (\S+))?(.*)$`)
	addrRE      = regexp.MustCompile(`^\s+(inet6?) (\S+)(.*)$`)
	lftRE       = regexp.MustCompile(`^\s+valid_lft (\S+) preferred_lft (\S+)`)
)

// ipObject picks the object the invocation addresses, tolerating abbreviations.
func ipObject(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "a", "ad", "add", "addr", "address":
			return "addr"
		case "l", "li", "link":
			return "link"
		}
		return a
	}
	return ""
}

func renderIP(argv, lines []string, aggressive bool) result {
	args := argv[1:]
	if hasAny(args, "-j", "-json", "-o", "-oneline", "-s", "-stats", "-d", "-details", "-p", "-pretty", "-r", "-resolve") {
		return result{}
	}
	obj := ipObject(args)
	if obj != "addr" && obj != "link" {
		return result{}
	}
	brief := hasAny(args, "-br", "-brief")
	switch {
	case brief:
		return renderBrief(lines, obj, aggressive)
	case obj == "addr" || obj == "link":
		return renderFull(lines, obj, aggressive)
	}
	return result{}
}

type addr struct{ fam, cidr, extra, lft string }

type iface struct {
	idx, name, state, master, mtu string
	flags                         string
	link, linkExtra               string
	addrs                         []addr
	extra                         []string // unrecognised continuation lines, kept raw
	raw                           []string
}

func renderFull(lines []string, obj string, aggressive bool) result {
	var ifs []*iface
	var cur *iface
	var pre []string
	for _, l := range lines {
		if m := ifaceHeadRE.FindStringSubmatch(l); m != nil {
			cur = &iface{idx: m[1], name: m[2], flags: m[3], mtu: m[4], raw: []string{l}}
			rest := m[5]
			if s := stateRE.FindStringSubmatch(rest); s != nil {
				cur.state = s[1]
			}
			if s := masterRE.FindStringSubmatch(rest); s != nil {
				cur.master = s[1]
			}
			ifs = append(ifs, cur)
			continue
		}
		if cur == nil {
			pre = append(pre, l)
			continue
		}
		cur.raw = append(cur.raw, l)
		switch {
		case linkRE.MatchString(l):
			m := linkRE.FindStringSubmatch(l)
			cur.link = m[1] + " " + m[2]
			cur.linkExtra = strings.TrimSpace(stripBrd(m[3]))
		case addrRE.MatchString(l):
			m := addrRE.FindStringSubmatch(l)
			cur.addrs = append(cur.addrs, addr{fam: m[1], cidr: m[2], extra: addrExtra(m[3], cur.name, m[2])})
		case lftRE.MatchString(l):
			m := lftRE.FindStringSubmatch(l)
			if len(cur.addrs) > 0 && !(m[1] == "forever" && m[2] == "forever") {
				cur.addrs[len(cur.addrs)-1].lft = "valid=" + m[1] + ",pref=" + m[2]
			}
		case strings.TrimSpace(l) == "":
		default:
			cur.extra = append(cur.extra, strings.TrimSpace(l))
		}
	}
	if len(ifs) == 0 {
		return result{}
	}
	if !aggressive {
		return result{}
	}
	var out []string
	out = append(out, pre...)
	var quiet []string
	elided := 0
	for _, f := range ifs {
		if f.state == "DOWN" && len(f.addrs) == 0 && len(f.extra) == 0 && zeroLink(f.link) {
			quiet = append(quiet, f.name)
			elided += len(f.raw) - 1
			continue
		}
		out = append(out, f.line(obj))
		elided += len(f.raw) - 1
	}
	if len(quiet) > 0 {
		out = append(out, fmt.Sprintf("DOWN, no address, zero MAC ×%d: %s", len(quiet), strings.Join(quiet, " ")))
	}
	return result{out: out, elided: elided, ok: true}
}

func stripBrd(s string) string {
	f := strings.Fields(s)
	var out []string
	for i := 0; i < len(f); i++ {
		if f[i] == "brd" && i+1 < len(f) {
			i++
			continue
		}
		out = append(out, f[i])
	}
	return strings.Join(out, " ")
}

// addrExtra keeps the address qualifiers that are not derivable: scope other
// than global, flags (dynamic, secondary, tentative, noprefixroute), labels
// that differ from the interface name.
func addrExtra(s, ifname, cidr string) string {
	if i := strings.IndexByte(ifname, '@'); i > 0 {
		ifname = ifname[:i]
	}
	f := strings.Fields(s)
	var out []string
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "brd":
			i++
		case "scope":
			if i+1 < len(f) && f[i+1] != "global" && !impliedScope(f[i+1], cidr) {
				out = append(out, "scope="+f[i+1])
			}
			i++
		default:
			if f[i] != ifname {
				out = append(out, f[i])
			}
		}
	}
	return strings.Join(out, " ")
}

// impliedScope reports scopes that follow from the address itself: fe80::/10 is
// link scope and loopback is host scope.
func impliedScope(scope, cidr string) bool {
	c := strings.ToLower(cidr)
	return scope == "link" && strings.HasPrefix(c, "fe80:") ||
		scope == "host" && (strings.HasPrefix(c, "127.") || strings.HasPrefix(c, "::1/"))
}

func zeroLink(l string) bool {
	f := strings.Fields(l)
	if len(f) < 2 {
		return true
	}
	for _, c := range f[1] {
		if c != '0' && c != ':' && c != '.' {
			return false
		}
	}
	return true
}

func (f *iface) line(obj string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s %s mtu %s", f.idx, f.name, f.state, f.mtu)
	if f.master != "" {
		b.WriteString(" master " + f.master)
	}
	if strings.Contains(f.flags, "NO-CARRIER") {
		b.WriteString(" NO-CARRIER")
	}
	if !zeroLink(f.link) {
		kind := strings.Fields(f.link)[0]
		if kind == "ether" {
			kind = "mac"
		}
		b.WriteString(" " + kind + " " + strings.Fields(f.link)[1])
	}
	if f.linkExtra != "" {
		b.WriteString(" " + f.linkExtra)
	}
	for _, a := range f.addrs {
		b.WriteString(" " + a.cidr)
		if a.extra != "" {
			b.WriteString("(" + a.extra + ")")
		}
		if a.lft != "" {
			b.WriteString("{" + a.lft + "}")
		}
	}
	for _, e := range f.extra {
		b.WriteString(" | " + e)
	}
	return b.String()
}

// renderBrief handles `-br addr` and `-br link`: squeeze padding, fold DOWN
// interfaces that carry nothing, and fold interfaces whose only addresses are
// auto-generated fe80:: link-locals.
func renderBrief(lines []string, obj string, aggressive bool) result {
	var out, quiet, linkLocal []string
	elided := 0
	parsed := 0
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 2 || !isState(f[1]) {
			out = append(out, l)
			continue
		}
		parsed++
		name, state, rest := f[0], f[1], f[2:]
		if !aggressive {
			out = append(out, strings.Join(f, " "))
			continue
		}
		switch {
		case obj == "addr" && state == "DOWN" && len(rest) == 0:
			quiet = append(quiet, name)
		case obj == "link" && state == "DOWN" && (len(rest) == 0 || zeroMAC(rest[0])):
			quiet = append(quiet, name)
		case obj == "addr" && len(rest) > 0 && allLinkLocal(rest):
			linkLocal = append(linkLocal, name)
		default:
			out = append(out, strings.Join(f, " "))
		}
	}
	if parsed == 0 {
		return result{}
	}
	if len(quiet) > 0 {
		out = append(out, fmt.Sprintf("DOWN, nothing to show ×%d: %s", len(quiet), strings.Join(quiet, " ")))
		elided += len(quiet)
	}
	if len(linkLocal) > 0 {
		out = append(out, fmt.Sprintf("only fe80:: link-local address (omitted) ×%d: %s", len(linkLocal), strings.Join(linkLocal, " ")))
		elided += len(linkLocal)
	}
	return result{out: out, elided: elided, ok: true}
}

func isState(s string) bool {
	switch s {
	case "UP", "DOWN", "UNKNOWN", "LOWERLAYERDOWN", "DORMANT", "NOTPRESENT", "TESTING":
		return true
	}
	return false
}

func zeroMAC(s string) bool {
	if strings.HasPrefix(s, "<") {
		return true
	}
	for _, c := range s {
		if c != '0' && c != ':' && c != '.' {
			return false
		}
	}
	return true
}

func allLinkLocal(addrs []string) bool {
	for _, a := range addrs {
		if !strings.HasPrefix(strings.ToLower(a), "fe80:") {
			return false
		}
	}
	return true
}
