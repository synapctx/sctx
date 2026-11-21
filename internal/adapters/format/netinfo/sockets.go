package netinfo

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// sock is one row of an ss or netstat socket table.
type sock struct {
	netid, state, rq, sq, local, peer, proc string
	raw                                     string
}

// groupThreshold is how many connection rows a table may have before
// connections (never listeners) are grouped by endpoint.
const groupThreshold = 15

func isListenState(s string) bool {
	switch strings.ToUpper(s) {
	case "LISTEN", "UNCONN", "":
		return true
	}
	return false
}

func renderSockets(argv, lines []string, aggressive bool, tool string) result {
	args := argv[1:]
	if tool == "ss" && (hasAny(args, "-H", "--no-header", "-i", "--info", "-m", "--memory", "-o", "--options", "-e", "--extended", "-c", "--continuous", "-E", "--events", "-K", "--kill", "-D", "--diag", "-s", "--summary") ||
		shortCluster(args, "himoecEKDs")) {
		return result{}
	}
	if tool == "netstat" && (shortCluster(args, "rismgcMCFw") || hasAny(args, "--route", "--interfaces", "--statistics", "--masquerade", "--groups", "--continuous", "--cache", "--fib")) {
		return result{}
	}
	var (
		pre    []string
		header string
		socks  []sock
		tail   []string
		ok     bool
		unix   bool
	)
	for _, l := range lines {
		f := strings.Fields(l)
		switch {
		case len(f) == 0:
			continue
		case unix:
			tail = append(tail, l)
			continue
		case tool == "ss" && (f[0] == "Netid" || f[0] == "State"):
			header = strings.Join(f, " ")
			ok = true
			continue
		case tool == "netstat" && f[0] == "Proto":
			header = strings.Join(f, " ")
			ok = true
			continue
		case tool == "netstat" && strings.HasPrefix(l, "Active UNIX"):
			unix = true
			tail = append(tail, l)
			continue
		case !ok:
			pre = append(pre, l)
			continue
		}
		s, good := parseSock(tool, header, f)
		if !good {
			tail = append(tail, l)
			continue
		}
		s.raw = strings.Join(f, " ")
		socks = append(socks, s)
	}
	if !ok || len(socks) == 0 {
		return result{}
	}
	var out []string
	out = append(out, pre...)
	if header != "" {
		out = append(out, header)
	}
	var conns []sock
	for _, s := range socks {
		if isListenState(s.state) || !aggressive {
			out = append(out, s.raw)
		} else {
			conns = append(conns, s)
		}
	}
	elided := 0
	if len(conns) <= groupThreshold {
		for _, s := range conns {
			out = append(out, s.raw)
		}
	} else {
		g, e := groupConns(conns, socks)
		out = append(out, g...)
		elided = e
	}
	out = append(out, tail...)
	return result{out: out, elided: elided, ok: true}
}

// parseSock splits one table row. ss prints [Netid] State Recv-Q Send-Q Local
// Peer [Process]; netstat prints Proto Recv-Q Send-Q Local Foreign [State]
// [PID/Program], where UDP rows have no state.
func parseSock(tool, header string, f []string) (sock, bool) {
	if tool == "ss" {
		i := 0
		var s sock
		if strings.HasPrefix(header, "Netid") {
			s.netid = f[0]
			i = 1
		}
		if len(f) < i+5 {
			return s, false
		}
		s.state, s.rq, s.sq, s.local, s.peer = f[i], f[i+1], f[i+2], f[i+3], f[i+4]
		s.proc = strings.Join(f[i+5:], " ")
		return s, true
	}
	if len(f) < 5 {
		return sock{}, false
	}
	s := sock{netid: f[0], rq: f[1], sq: f[2], local: f[3], peer: f[4]}
	rest := f[5:]
	if len(rest) > 0 && isUpperWord(rest[0]) {
		s.state, rest = rest[0], rest[1:]
	}
	s.proc = strings.Join(rest, " ")
	return s, true
}

func isUpperWord(s string) bool {
	for _, c := range s {
		if (c < 'A' || c > 'Z') && c != '_' && c != '-' {
			return false
		}
	}
	return s != ""
}

// splitHostPort splits at the last colon; IPv6 addresses keep their brackets.
func splitHostPort(a string) (host, port string) {
	i := strings.LastIndex(a, ":")
	if i < 0 {
		return a, ""
	}
	return a[:i], a[i+1:]
}

// groupConns folds connection rows by (netid, state, side, fixed endpoint,
// process). The fixed endpoint is the side that is a listening port on this
// host (inbound) or else the remote side (outbound); the other side's ephemeral
// ports are dropped and its hosts counted.
func groupConns(conns, all []sock) ([]string, int) {
	listen := map[string]bool{}
	for _, s := range all {
		if isListenState(s.state) {
			_, p := splitHostPort(s.local)
			listen[p] = true
		}
	}
	type key struct{ netid, state, side, fixed, proc string }
	rawOf := map[key]string{}
	type agg struct {
		n     int
		other map[string]int
	}
	groups := map[key]*agg{}
	var order []key
	for _, s := range conns {
		_, lp := splitHostPort(s.local)
		k := key{netid: s.netid, state: s.state, proc: procNames(s.proc)}
		var other string
		if listen[lp] {
			k.side, k.fixed = "in", s.local
			other, _ = splitHostPort(s.peer)
		} else {
			k.side, k.fixed = "out", s.peer
			other, _ = splitHostPort(s.local)
		}
		g := groups[k]
		if g == nil {
			rawOf[k] = s.raw
			g = &agg{other: map[string]int{}}
			groups[k] = g
			order = append(order, k)
		}
		g.n++
		g.other[other]++
	}
	sort.SliceStable(order, func(i, j int) bool { return groups[order[i]].n > groups[order[j]].n })
	out := []string{fmt.Sprintf("connections grouped by endpoint (%d rows):", len(conns))}
	for _, k := range order {
		g := groups[k]
		if g.n == 1 {
			out = append(out, rawOf[k]) // a singleton keeps its full row, pid and ports included
			continue
		}
		line := fmt.Sprintf("%s %s %s %s ×%d", k.netid, k.state, k.side, k.fixed, g.n)
		line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		line += " " + topHosts(g.other, 3)
		if k.proc != "" {
			line += " " + k.proc
		}
		out = append(out, line)
	}
	singles := 0
	for _, k := range order {
		if groups[k].n == 1 {
			singles++
		}
	}
	return out, len(conns) - len(order) + singles
}

var netstatProcRE = regexp.MustCompile(`^\d+/(\S+)$`)

var procNameRE = regexp.MustCompile(`\("([^"]+)",pid=`)

// procNames reduces ss's users:(("nc",pid=1273,fd=3)) to "nc" so that sockets
// of one program group together; the pids are what made every row distinct.
func procNames(p string) string {
	var names []string
	for _, m := range procNameRE.FindAllStringSubmatch(p, -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		if m := netstatProcRE.FindStringSubmatch(p); m != nil {
			return "(" + m[1] + ")" // netstat -p: PID/Program name
		}
		return p
	}
	return "(" + strings.Join(names, ",") + ")"
}

func topHosts(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range m {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	if len(kvs) == 1 {
		return "from/to " + kvs[0].k
	}
	var parts []string
	for i, e := range kvs {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%d more hosts", len(kvs)-n))
			break
		}
		parts = append(parts, fmt.Sprintf("%s×%d", e.k, e.v))
	}
	return "from/to " + strings.Join(parts, ",")
}
