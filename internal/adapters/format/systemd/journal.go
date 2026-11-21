package systemd

import (
	"fmt"
	"regexp"
	"strings"
)

// journalRE matches the `short` / `short-iso` / `short-precise` line grammar:
// timestamp, host, then `ident[pid]: message`.
var journalRE = regexp.MustCompile(`^((?:[A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d(?:\.\d+)?)|(?:\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?)) (\S+) (.+)$`)

var (
	digitsRE = regexp.MustCompile(`\d+`)
	// errorishRE marks lines whose digits can be the point (exit codes, errnos,
	// attempt counters) and which are therefore never shape-folded.
	errorishRE = regexp.MustCompile(`(?i)(error|fail|fatal|panic|critical|denied|refused|timed? ?out|timeout|exception|segfault|oom|killed|status=|exit|abort|crash|unreachable|invalid|cannot|can't|unable)`)
)

type jline struct {
	raw, ts, host, rest string
	ok                  bool
}

func parseJournal(l string) jline {
	m := journalRE.FindStringSubmatch(l)
	if m == nil {
		return jline{raw: l}
	}
	return jline{raw: l, ts: m[1], host: m[2], rest: m[3], ok: true}
}

// uniformHost returns the single host shared by every matched line, if any.
func uniformHost(js []jline) (string, bool) {
	host := ""
	for _, j := range js {
		if !j.ok {
			continue
		}
		if host == "" {
			host = j.host
		} else if host != j.host {
			return "", false
		}
	}
	return host, host != ""
}

var hexRE = regexp.MustCompile(`[0-9a-f]{7,}`)

// shape masks hex identifiers (container and veth names) and digits.
func shape(rest string) string {
	return digitsRE.ReplaceAllString(hexRE.ReplaceAllString(rest, "#"), "#")
}

// timeOnly returns the clock part of a timestamp when both ends share the date.
func rangeTS(first, last string) string {
	if first == last {
		return first
	}
	if len(first) >= 6 && len(last) >= 6 && first[:6] == last[:6] && strings.Count(first, ":") == 2 && !strings.Contains(first, "T") {
		return first + "…" + last[7:]
	}
	return first + "…" + last
}

// collapseJournal folds one run of journal lines. aggressive enables the
// lossy shape fold. elided counts lines removed.
func collapseJournal(js []jline, stripHost, aggressive bool) (out []string, elided int) {
	text := func(j jline) string {
		if !j.ok {
			return j.raw
		}
		if stripHost {
			return j.ts + " " + j.rest
		}
		return j.ts + " " + j.host + " " + j.rest
	}
	for i := 0; i < len(js); {
		j := js[i]
		if !j.ok {
			out = append(out, j.raw)
			i++
			continue
		}
		k := i + 1
		for k < len(js) && js[k].ok && js[k].host == j.host && js[k].rest == j.rest {
			k++
		}
		if k-i >= 3 {
			prefix := ""
			if !stripHost {
				prefix = j.host + " "
			}
			out = append(out, fmt.Sprintf("%s %s%s ×%d", rangeTS(j.ts, js[k-1].ts), prefix, j.rest, k-i))
			elided += k - i - 1
			i = k
			continue
		}
		if aggressive && !errorishRE.MatchString(j.rest) {
			s := shape(j.rest)
			m := i + 1
			for m < len(js) && js[m].ok && js[m].host == j.host && !errorishRE.MatchString(js[m].rest) && shape(js[m].rest) == s {
				m++
			}
			if m-i >= 5 {
				out = append(out, text(j), fmt.Sprintf("…+%d similar lines (digits vary)", m-i-2), text(js[m-1]))
				elided += m - i - 2
				i = m
				continue
			}
		}
		out = append(out, text(j))
		i++
	}
	return out, elided
}

func parseAll(lines []string) []jline {
	js := make([]jline, len(lines))
	for i, l := range lines {
		js[i] = parseJournal(l)
	}
	return js
}

func renderJournal(lines []string, aggressive bool) result {
	js := parseAll(lines)
	matched := 0
	for _, j := range js {
		if j.ok {
			matched++
		}
	}
	host, uniform := uniformHost(js)
	strip := aggressive && uniform
	out, elided := collapseJournal(js, strip, aggressive)
	if strip {
		out = append([]string{"host: " + host}, out...)
	}
	return result{out: out, elided: elided, ok: matched > 0 || elided > 0}
}
