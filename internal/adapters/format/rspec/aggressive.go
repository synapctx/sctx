package rspec

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	summaryRE   = regexp.MustCompile(`^(\d+) examples?, (\d+) failures?`)
	entryRE     = regexp.MustCompile(`^\s{2}\d+\) (.+)$`)
	rerunRE     = regexp.MustCompile(`^rspec (\S+)(?: # .*)?$`)
	blockInRE   = regexp.MustCompile(`:in '(?:block[^']*|<top \(required\)>)'$`)
	locRE       = regexp.MustCompile(`^# (\S+:\d+)`)
	gemFrameRE  = regexp.MustCompile(`/gems/|/vendor/bundle/|\(gem\)|/lib/ruby/`)
	finishedRE  = regexp.MustCompile(`^Finished in `)
	outsideRE   = regexp.MustCompile(`errors? occurred outside of examples`)
	optionsRE   = regexp.MustCompile(`^Run options:`)
	compareNote = "(compared using"
)

type section int

const (
	secPre section = iota
	secFailures
	secPending
	secRerun
	secOther
)

// aggressiveRender keeps what a developer acts on: each failure (message,
// diff, backtrace frames in the project), a combined rerun command and the
// summary line. It declines when the output carries no rspec summary, or when
// the summary reports failures this parser could not find.
func aggressiveRender(argv []string, stdout, stderr string) (format.Rendered, error) {
	lines := splitLines(stripANSI(stdout))
	var summary string
	failedReported := 0
	for _, l := range lines {
		if m := summaryRE.FindStringSubmatch(l); m != nil {
			summary = l
			failedReported, _ = strconv.Atoi(m[2])
		}
		if outsideRE.MatchString(l) && summary != "" {
			// "0 examples, 0 failures, 1 error occurred outside of examples"
			failedReported++
		}
	}
	if summary == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	doc := docFormat(argv)
	if doc && hasOutsideError(lines) {
		// The documentation tree and a load-error block are both indented
		// prose; telling them apart is a guess, so leave it to the relaxed tier.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var out []string
	var rerun []string
	var pend []pending
	sec := secPre
	failuresSeen := 0
	treeDropped := 0
	var cur *pending
	gem := 0
	flushGem := func() {
		if gem > 0 {
			out = append(out, fmt.Sprintf("  # …+%d gem frames", gem))
			gem = 0
		}
	}
	flushPend := func() {
		if cur != nil {
			pend = append(pend, *cur)
			cur = nil
		}
	}

	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		t := strings.TrimSpace(l)
		switch {
		case t == "Failures:":
			flushGem()
			flushPend()
			sec = secFailures
			continue
		case strings.HasPrefix(t, "Pending:"):
			flushGem()
			sec = secPending
			continue
		case t == "Failed examples:":
			flushGem()
			flushPend()
			sec = secRerun
			continue
		case finishedRE.MatchString(t):
			flushGem()
			flushPend()
			sec = secOther
			continue
		case t == "":
			continue
		}
		if m := summaryRE.FindStringSubmatch(t); m != nil {
			flushGem()
			flushPend()
			sec = secOther
			continue // emitted last
		}

		switch sec {
		case secPre:
			if progressRowRE.MatchString(t) || optionsRE.MatchString(t) {
				continue
			}
			if doc && !strings.Contains(t, ".rb") {
				treeDropped++
				continue
			}
			out = append(out, l)
		case secFailures:
			if m := entryRE.FindStringSubmatch(l); m != nil {
				flushGem()
				failuresSeen++
				out = append(out, fmt.Sprintf("%d) %s", failuresSeen, m[1]))
				continue
			}
			if strings.HasPrefix(t, compareNote) {
				continue
			}
			if strings.HasPrefix(t, "# ") && sec == secFailures {
				if gemFrameRE.MatchString(t) {
					gem++
					continue
				}
				flushGem()
				out = append(out, "  "+blockInRE.ReplaceAllString(t, ""))
				continue
			}
			flushGem()
			out = append(out, "  "+trimIndent(l, 5))
		case secPending:
			if m := entryRE.FindStringSubmatch(l); m != nil {
				flushPend()
				cur = &pending{title: m[1]}
				continue
			}
			if cur == nil {
				continue
			}
			if strings.HasPrefix(t, "# ") {
				if cur.reason == "" && cur.dropped == 0 && !locRE.MatchString(t) {
					cur.reason = strings.TrimPrefix(t, "# ")
					continue
				}
				if m := locRE.FindStringSubmatch(t); m != nil {
					cur.loc = m[1]
					continue
				}
			}
			cur.dropped++
		case secRerun:
			if m := rerunRE.FindStringSubmatch(t); m != nil {
				rerun = append(rerun, m[1])
				continue
			}
			out = append(out, l)
		case secOther:
			if strings.HasPrefix(t, "Randomized with seed") {
				out = append(out, t)
				continue
			}
			out = append(out, l)
		}
	}
	flushGem()
	flushPend()

	if failedReported > 0 && failuresSeen == 0 && len(rerun) == 0 && !hasOutsideError(lines) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	if treeDropped > 0 {
		fmt.Fprintf(&b, "…+%d example lines\n", treeDropped)
	}
	for _, l := range out {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, p := range pend {
		b.WriteString("pending: " + p.title)
		if p.reason != "" {
			b.WriteString(" — " + p.reason)
		}
		if p.loc != "" {
			b.WriteString(" (" + p.loc + ")")
		}
		if p.dropped > 0 {
			fmt.Fprintf(&b, " …+%d lines", p.dropped)
		}
		b.WriteByte('\n')
	}
	if len(rerun) > 0 {
		b.WriteString("rerun: rspec " + strings.Join(rerun, " ") + "\n")
	}
	b.WriteString(summary + "\n")
	if stderr != "" {
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != "", Elided: true}, nil
}

type pending struct {
	title, reason, loc string
	dropped            int
}

func hasOutsideError(lines []string) bool {
	for _, l := range lines {
		if outsideRE.MatchString(l) {
			return true
		}
	}
	return false
}

// trimIndent removes up to n leading spaces.
func trimIndent(s string, n int) string {
	i := 0
	for i < n && i < len(s) && s[i] == ' ' {
		i++
	}
	return s[i:]
}
