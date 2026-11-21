package cargo

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	nextestEventRE = regexp.MustCompile(`^\s+([A-Z][A-Z -]*[A-Z]) \[\s*[\d.]+s\] (?:\(\s*\d+/\d+\) )?(.+)$`)
	nextestPassRE  = regexp.MustCompile(`^\s+(PASS|SLOW|LEAK|SKIP) \[`)
	nextestRuleRE  = regexp.MustCompile(`^─{6,}$`)
	nextestSumRE   = regexp.MustCompile(`^\s+Summary \[`)
	nextestSecRE   = regexp.MustCompile(`^\s+(stdout|stderr) ─+$`)
	libtestChatter = regexp.MustCompile(`^(running \d+ tests?|test .* \.\.\. (FAILED|ok)|failures:|test result: .*)$`)
)

// renderNextest compacts `cargo nextest run`: PASS events are counted, each
// failure keeps its event line and its captured output (libtest boilerplate
// in the stdout section dropped), and the trailing recap is dropped for
// failures already shown.
func renderNextest(stdout, stderr []byte) (format.Rendered, error) {
	all := splitLines(string(stderr))
	if len(stdout) > 0 {
		all = append(all, splitLines(string(stdout))...)
	}
	start := -1
	for i, l := range all {
		if nextestRuleRE.MatchString(strings.TrimSpace(l)) {
			start = i
			break
		}
	}
	if start < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	pre := newScan(false)
	pre.feed(all[:start])
	out := pre.out
	counts := pre.counts
	shown := map[string]bool{}
	sawSummary := false
	inSection := false
	afterFailures := false
	pendingHdr, stdoutShown := "", false
	for _, l := range all[start:] {
		t := strings.TrimSpace(l)
		switch {
		case nextestRuleRE.MatchString(t):
			inSection = false
			continue
		case strings.HasPrefix(t, "Nextest run ID"), strings.HasPrefix(t, "Starting "):
			counts["run header"]++
			inSection = false
			continue
		case nextestSumRE.MatchString(l):
			sawSummary = true
			inSection = false
			out = append(out, t)
			continue
		case nextestPassRE.MatchString(l):
			counts["pass/skip"]++
			inSection = false
			continue
		}
		if m := nextestEventRE.FindStringSubmatch(l); m != nil {
			inSection = false
			if sawSummary && shown[m[2]] {
				counts["recap"]++
				continue
			}
			shown[m[2]] = true
			stdoutShown, pendingHdr = false, ""
			out = append(out, m[1]+" "+m[2])
			continue
		}
		if nextestSecRE.MatchString(l) {
			inSection = true
			pendingHdr = ""
			if strings.Contains(l, "stdout") || stdoutShown {
				pendingHdr = strings.TrimSpace(l)
			}
			continue
		}
		if inSection {
			d := strings.TrimPrefix(l, "    ")
			dt := strings.TrimSpace(d)
			switch {
			case dt == "":
				continue
			case afterFailures && dt == "failures:":
				counts["libtest chatter"]++
				continue
			case afterFailures && !strings.HasPrefix(dt, "test result"):
				afterFailures = false
				counts["libtest chatter"]++
				continue
			case hintRE.MatchString(dt):
				counts["backtrace hint"]++
				continue
			case libtestChatter.MatchString(dt):
				afterFailures = dt == "failures:"
				counts["libtest chatter"]++
				continue
			case strings.HasPrefix(dt, "test ") && strings.HasSuffix(dt, "FAILED"):
				counts["libtest chatter"]++
				continue
			}
			if pendingHdr != "" {
				out = append(out, pendingHdr)
				stdoutShown = stdoutShown || strings.HasPrefix(pendingHdr, "stdout")
				pendingHdr = ""
			}
			if m := threadIDRE.FindStringSubmatch(d); m != nil {
				d = m[1] + m[2]
			}
			out = append(out, "  "+d)
			continue
		}
		if t != "" {
			out = append(out, l)
		}
	}
	n := 0
	var parts []string
	for _, k := range append(append([]string{}, markerOrder...), "run header", "pass/skip", "libtest chatter", "recap") {
		if c := counts[k]; c > 0 {
			n += c
			parts = append(parts, fmt.Sprintf("%s ×%d", k, c))
		}
	}
	var b strings.Builder
	for _, l := range out {
		b.WriteString(l + "\n")
	}
	if n > 0 {
		fmt.Fprintf(&b, "…+%d lines (%s)\n", n, strings.Join(parts, ", "))
	}
	if strings.TrimSpace(b.String()) == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: true, Elided: n > 0}, nil
}
