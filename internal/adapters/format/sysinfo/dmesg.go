package sysinfo

import (
	"fmt"
	"regexp"
)

var (
	// dmesgLineRE matches `[  123.456789] msg`, `[Fri Nov 20 22:54:46 2026] msg`
	// and the -x form `kern  :info  : [ 123.4] msg`.
	dmesgLineRE = regexp.MustCompile(`^((?:\w+\s*:\w+\s*: )?\[[^\]]*\]) (.*)$`)
	numRE       = regexp.MustCompile(`\d+`)
	hexRE       = regexp.MustCompile(`[0-9a-f]{7,}`)
	macRE       = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?::[0-9a-f]{2}){5}\b`)
	errorishRE  = regexp.MustCompile(`(?i)(error|fail|fatal|panic|oops|bug:|warn|critical|denied|refused|timeout|timed out|exception|segfault|oom|out of memory|killed|abort|corrupt|unreachable|invalid|call trace|i/o|blocked for more than)`)
)

func dmesgShape(msg string) string {
	return numRE.ReplaceAllString(hexRE.ReplaceAllString(macRE.ReplaceAllString(msg, "#"), "#"), "#")
}

// renderDmesg shows each distinct message once, at its first occurrence, with
// the repeat count and the time of the last occurrence. Messages that look like
// errors group only when byte-identical; everything else groups by shape (digits,
// hex identifiers and MAC addresses masked), which is what folds a container
// churn of `vethNNN: entered disabled state` into one line.
func renderDmesg(args, lines []string, aggressive bool) result {
	if hasFlag(args, "-w", "--follow", "-W", "--follow-new", "-J", "--json", "-c", "--read-clear", "-C", "--clear", "-D", "--console-off", "-E", "--console-on", "-n", "--console-level", "-r", "--raw") ||
		shortCluster(args, "wWJcCDEnr") {
		return result{}
	}
	type entry struct {
		prefix, msg, lastPrefix string
		count                   int
		line                    int
	}
	var entries []*entry
	byKey := map[string]*entry{}
	var out []string
	parsed := 0
	for _, l := range lines {
		m := dmesgLineRE.FindStringSubmatch(l)
		if m == nil {
			out = append(out, l) // continuation lines and call-trace frames stay in place
			continue
		}
		parsed++
		if !aggressive {
			out = append(out, l)
			continue
		}
		key := dmesgShape(m[2])
		if errorishRE.MatchString(m[2]) {
			key = "=" + m[2]
		}
		if e := byKey[key]; e != nil {
			e.count++
			e.lastPrefix = m[1]
			continue
		}
		e := &entry{prefix: m[1], msg: m[2], lastPrefix: m[1], count: 1, line: len(out)}
		byKey[key] = e
		entries = append(entries, e)
		out = append(out, "") // placeholder, filled once the counts are known
	}
	if parsed == 0 {
		return result{}
	}
	if !aggressive {
		return result{} // nothing lossless to do to dmesg lines
	}
	elided := 0
	for _, e := range entries {
		if e.count == 1 {
			out[e.line] = e.prefix + " " + e.msg
			continue
		}
		out[e.line] = fmt.Sprintf("%s %s ×%d (last %s)", e.prefix, e.msg, e.count, e.lastPrefix)
		elided += e.count - 1
	}
	if elided == 0 {
		return result{}
	}
	return result{out: out, elided: elided, ok: true}
}
