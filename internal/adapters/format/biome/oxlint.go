package biome

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	oxHeadRE    = regexp.MustCompile(`^  ([x×!]) (.+)$`)
	oxLocRE     = regexp.MustCompile(`^\s+,-\[(.+?):(\d+):(\d+)\]$`)
	oxRuleRE    = regexp.MustCompile(`^([\w@/.-]+\([^)]*\)|[\w@/.-]+): (.*)$`)
	oxCompactRE = regexp.MustCompile(`^(\S+?):(\d+):(\d+): (warning|error) (.+)$`)
	oxFoundRE   = regexp.MustCompile(`^Found (\d+) warnings? and (\d+) errors?\.$`)
	oxFinishRE  = regexp.MustCompile(`^Finished in .* on (\d+) files?`)
)

func oxSplit(s string) (rule, msg string) {
	if m := oxRuleRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2]
	}
	return "", s
}

func oxSev(g string) string {
	if g == "!" {
		return "warn"
	}
	return "error"
}

// parseOxlint reads either the default multi-line reporter or the one-line
// form oxlint switches to when it detects an AI agent environment.
func parseOxlint(lines []string) (report, bool) {
	var r report
	files := map[string]bool{}
	add := func(d diag) {
		r.diags = append(r.diags, d)
		files[d.file] = true
		if d.sev == "error" {
			r.errs++
		} else {
			r.warns++
		}
	}
	var pending *diag // header seen, waiting for its location line
	foundW, foundE := -1, -1
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if m := oxCompactRE.FindStringSubmatch(l); m != nil {
			rule, msg := oxSplit(m[5])
			sev := "error"
			if m[4] == "warning" {
				sev = "warn"
			}
			if i := strings.Index(msg, " help: "); i >= 0 {
				msg = msg[:i]
				r.hidden++
			}
			add(diag{file: m[1], pos: m[2] + ":" + m[3], rule: rule, msg: msg, sev: sev})
			continue
		}
		if m := oxHeadRE.FindStringSubmatch(l); m != nil {
			rule, msg := oxSplit(m[2])
			pending = &diag{rule: rule, msg: msg, sev: oxSev(m[1])}
			continue
		}
		if m := oxLocRE.FindStringSubmatch(l); m != nil && pending != nil {
			pending.file, pending.pos = m[1], m[2]+":"+m[3]
			add(*pending)
			pending = nil
			continue
		}
		if m := oxFoundRE.FindStringSubmatch(t); m != nil {
			foundW, _ = strconv.Atoi(m[1])
			foundE, _ = strconv.Atoi(m[2])
			continue
		}
		if oxFinishRE.MatchString(t) {
			continue
		}
		if t != "" {
			r.hidden++
		}
	}
	if pending != nil || len(r.diags) == 0 {
		return report{}, false
	}
	if foundW >= 0 {
		if foundW != r.warns || foundE != r.errs {
			return report{}, false
		}
	}
	r.files = len(files)
	return r, true
}
