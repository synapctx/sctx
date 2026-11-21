package sysinfo

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// renderLsblk handles the default column set only: every column is populated
// except a trailing MOUNTPOINTS, so squeezing padding cannot shift a field.
func renderLsblk(args, lines []string, aggressive bool) result {
	if hasFlag(args, "-f", "--fs", "-o", "--output", "-O", "--output-all", "-J", "--json", "-P", "--pairs", "-r", "--raw", "-n", "--noheadings", "-p", "--paths", "-x", "--sort") ||
		shortCluster(args, "foOJPrnpxbmdt") && !shortCluster(args, "a") {
		return result{}
	}
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "NAME") {
		return result{}
	}
	cols := strings.Fields(lines[0])
	want := []string{"NAME", "MAJ:MIN", "RM", "SIZE", "RO", "TYPE"}
	if len(cols) < len(want) {
		return result{}
	}
	for i, w := range want {
		if cols[i] != w {
			return result{}
		}
	}
	out := []string{squeeze(lines[0])}
	var empty []string
	loops := []string{}
	elided := 0
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] == ' ' { // extra mountpoint of the row above
			out = append(out, "  "+strings.TrimSpace(l))
			continue
		}
		f := strings.Fields(l)
		if len(f) < len(want) {
			out = append(out, l)
			continue
		}
		name, size, typ := f[0], f[3], f[5]
		mounts := strings.Join(f[6:], " ")
		if aggressive && typ == "loop" {
			item := name
			if mounts != "" {
				item += "=" + mounts
			}
			loops = append(loops, item)
			elided++
			continue
		}
		if aggressive && size == "0B" && mounts == "" && (typ == "disk" || typ == "rom") {
			empty = append(empty, name)
			elided++
			continue
		}
		out = append(out, strings.Join(f, " "))
	}
	if len(empty) > 0 {
		out = append(out, fmt.Sprintf("0B devices without mounts ×%d: %s", len(empty), rangeNames(empty)))
	}
	if len(loops) > 0 {
		out = append(out, fmt.Sprintf("loop devices ×%d: %s", len(loops), strings.Join(loops, " ")))
	}
	return result{out: out, elided: elided, ok: true}
}

var trailingDigitsRE = regexp.MustCompile(`^(.*?)(\d+)$`)

// rangeNames renders nbd0 nbd1 ... nbd15 as nbd0-nbd15; names that do not form
// a consecutive numeric run are listed as they are.
func rangeNames(names []string) string {
	var out []string
	for i := 0; i < len(names); {
		m := trailingDigitsRE.FindStringSubmatch(names[i])
		j := i + 1
		if m != nil {
			n, _ := strconv.Atoi(m[2])
			for j < len(names) {
				mj := trailingDigitsRE.FindStringSubmatch(names[j])
				if mj == nil || mj[1] != m[1] {
					break
				}
				nj, _ := strconv.Atoi(mj[2])
				if nj != n+(j-i) {
					break
				}
				j++
			}
		}
		if j-i >= 3 {
			out = append(out, names[i]+"-"+names[j-1])
		} else {
			out = append(out, names[i:j]...)
		}
		i = j
	}
	return strings.Join(out, " ")
}
