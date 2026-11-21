package systemd

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// statusHeadRE is the first line of a unit block: bullet, name, " - ", text.
	statusHeadRE = regexp.MustCompile(`^[●○×↻◌⏺] \S+( - .*)?$`)
	treeRE       = regexp.MustCompile(`^\s+[│├└]`)
	docsRE       = regexp.MustCompile(`^\s+Docs:`)
	invocationRE = regexp.MustCompile(`^\s+Invocation:`)
)

// maxTreeRows is how many CGroup process rows survive; a forking server can
// print hundreds.
const maxTreeRows = 6

type statusBlock struct {
	meta    []string
	journal []string
}

func renderStatus(lines []string, aggressive bool) result {
	var (
		pre    []string // lines before the first unit block (e.g. "Unit x could not be found.")
		blocks []*statusBlock
		cur    *statusBlock
		inJrnl bool
	)
	for _, l := range lines {
		switch {
		case statusHeadRE.MatchString(l):
			cur = &statusBlock{meta: []string{l}}
			blocks = append(blocks, cur)
			inJrnl = false
		case cur == nil:
			pre = append(pre, l)
		case strings.TrimSpace(l) == "":
			inJrnl = true
		case inJrnl:
			cur.journal = append(cur.journal, l)
		default:
			cur.meta = append(cur.meta, l)
		}
	}
	if len(blocks) == 0 {
		return result{}
	}
	var all []jline
	for _, b := range blocks {
		all = append(all, parseAll(b.journal)...)
	}
	host, uniform := uniformHost(all)
	strip := aggressive && uniform

	var out []string
	elided := 0
	if strip {
		out = append(out, "host: "+host)
	}
	out = append(out, pre...)
	for bi, b := range blocks {
		if bi > 0 {
			out = append(out, "")
		}
		meta, e := filterMeta(b.meta, aggressive)
		elided += e
		out = append(out, meta...)
		if len(b.journal) > 0 {
			j, e := collapseJournal(parseAll(b.journal), strip, aggressive)
			elided += e
			out = append(out, j...)
		}
	}
	return result{out: out, elided: elided, ok: true}
}

// filterMeta drops the lines of a status block that never decide anything
// (Docs, Invocation) and truncates a long CGroup process tree.
func filterMeta(meta []string, aggressive bool) ([]string, int) {
	if !aggressive {
		return meta, 0
	}
	var out []string
	elided, tree := 0, 0
	skipping := false // inside the continuation of a dropped key
	for _, l := range meta {
		switch {
		case docsRE.MatchString(l), invocationRE.MatchString(l):
			skipping = true
			elided++
			continue
		case skipping && strings.HasPrefix(l, "             ") && !treeRE.MatchString(l):
			elided++ // a Docs: continuation (man:... or https://... entries)
			continue
		}
		skipping = false
		if treeRE.MatchString(l) {
			tree++
			if tree > maxTreeRows {
				elided++
				continue
			}
		}
		out = append(out, l)
	}
	if tree > maxTreeRows {
		out = append(out, fmt.Sprintf("             …+%d more CGroup rows", tree-maxTreeRows))
	}
	return out, elided
}
