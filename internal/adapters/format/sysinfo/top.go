package sysinfo

import (
	"fmt"
	"sort"
	"strings"
)

// topRows is how many process rows survive, in the order top printed them.
const topRows = 15

// renderTop handles one batch iteration (`top -b -n 1`). Several iterations, or
// output that is not top's batch layout, decline.
func renderTop(args, lines []string, aggressive bool) result {
	if !shortCluster(args, "b") || !(shortCluster(args, "n") || hasFlag(args, "--iterations")) {
		return result{} // interactive or following: streamsForever keeps it unwrapped
	}
	hdr := -1
	tops := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "top - ") {
			tops++
		}
		if hdr < 0 && strings.Contains(l, "PID") && strings.Contains(l, "COMMAND") {
			hdr = i
		}
	}
	if hdr < 0 || tops != 1 {
		return result{}
	}
	cols := strings.Fields(lines[hdr])
	idx := map[string]int{}
	for i, c := range cols {
		idx[c] = i
	}
	cmdI, okC := idx["COMMAND"]
	sI, okS := idx["S"]
	if !okC || !okS || cmdI != len(cols)-1 {
		return result{}
	}

	var out []string
	for _, l := range lines[:hdr] {
		if strings.TrimSpace(l) != "" {
			out = append(out, squeeze(l))
		}
	}
	out = append(out, squeeze(lines[hdr]))

	type row struct {
		text, cmd, state string
	}
	var rows []row
	for _, l := range lines[hdr+1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.Fields(l)
		if len(f) < len(cols) {
			rows = append(rows, row{text: squeeze(l), cmd: "?"})
			continue
		}
		cmd := strings.Join(f[cmdI:], " ")
		rows = append(rows, row{text: strings.Join(f[:cmdI], " ") + " " + cmd, cmd: f[cmdI], state: f[sI]})
	}
	if !aggressive || len(rows) <= topRows {
		for _, r := range rows {
			out = append(out, r.text)
		}
		return result{out: out, ok: true}
	}
	rest := map[string]int{}
	elided := 0
	for i, r := range rows {
		// Rows past the cut survive when they are blocked (D) or zombie (Z): the
		// two states an operator reads top to find.
		if i < topRows || r.state == "D" || r.state == "Z" {
			out = append(out, r.text)
			continue
		}
		rest[r.cmd]++
		elided++
	}
	out = append(out, fmt.Sprintf("…+%d more processes (%s)", elided, topCommands(rest, 6)))
	return result{out: out, elided: elided, ok: true}
}

func topCommands(m map[string]int, n int) string {
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
	var parts []string
	for i, e := range kvs {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%d other commands", len(kvs)-n))
			break
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", e.k, e.v))
	}
	return strings.Join(parts, ", ")
}
