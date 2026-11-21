package rubocop

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	// emacs-style (default "progress") offense header.
	offenseRE = regexp.MustCompile(`^(\S.*?):(\d+):(\d+): ([CWEFR]): (?:\[(Correctable|Corrected|Todo)\] )?([A-Z]\w*(?:/\w+)*): (.*)$`)
	// "simple" formatter rows, grouped under a "== file ==" heading.
	simpleRE   = regexp.MustCompile(`^([CWEFR]): +(\d+): +(\d+): (?:\[(Correctable|Corrected|Todo)\] )?([A-Z]\w*(?:/\w+)*): (.*)$`)
	fileHdrRE  = regexp.MustCompile(`^== (.+) ==$`)
	summaryRE  = regexp.MustCompile(`^\d+ files? inspected(?:, |$)`)
	inspectRE  = regexp.MustCompile(`^Inspecting \d+ files?$`)
	progressRE = regexp.MustCompile(`^[.CWEFA]+$`)
)

type offense struct {
	line, col int
	sev       string
	tag       string
	cop, msg  string
	extra     []string
}

// group folds offenses of one file that share severity, tag, cop and message.
type group struct {
	pos       []string
	sev, tag  string
	cop, msg  string
	extra     []string
	firstLine int
}

type fileOffenses struct {
	name string
	offs []offense
}

// aggressiveRender parses the offense report. It declines when no summary
// line is present or when the summary counts offenses the parser did not find.
func aggressiveRender(stdout, stderr string) (format.Rendered, error) {
	lines := splitLines(stripANSI(stdout))
	var files []*fileOffenses
	byName := map[string]*fileOffenses{}
	var summary string
	var cur *offense
	var curFile *fileOffenses
	skip := 0 // source + caret echo lines still to drop
	var preamble []string

	add := func(name string, o offense) *offense {
		f := byName[name]
		if f == nil {
			f = &fileOffenses{name: name}
			byName[name] = f
			files = append(files, f)
		}
		f.offs = append(f.offs, o)
		return &f.offs[len(f.offs)-1]
	}

	inOffenses := false
	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		if m := offenseRE.FindStringSubmatch(l); m != nil {
			o := offense{sev: m[4], tag: m[5], cop: m[6], msg: m[7]}
			fmt.Sscan(m[2], &o.line)
			fmt.Sscan(m[3], &o.col)
			cur = add(m[1], o)
			skip = 2
			inOffenses = true
			continue
		}
		if m := fileHdrRE.FindStringSubmatch(l); m != nil {
			curFile = &fileOffenses{name: m[1]}
			if byName[m[1]] == nil {
				byName[m[1]] = curFile
				files = append(files, curFile)
			} else {
				curFile = byName[m[1]]
			}
			inOffenses = true
			skip = 0
			continue
		}
		if m := simpleRE.FindStringSubmatch(l); m != nil && curFile != nil {
			o := offense{sev: m[1], tag: m[4], cop: m[5], msg: m[6]}
			fmt.Sscan(m[2], &o.line)
			fmt.Sscan(m[3], &o.col)
			curFile.offs = append(curFile.offs, o)
			cur = &curFile.offs[len(curFile.offs)-1]
			continue
		}
		if summaryRE.MatchString(l) {
			summary = l
			cur = nil
			continue
		}
		if skip > 0 {
			skip-- // source line and caret line echoed under each offense
			continue
		}
		if l == "Offenses:" || l == "" || inspectRE.MatchString(l) || progressRE.MatchString(l) {
			continue
		}
		if cur != nil && inOffenses && summary == "" && strings.HasPrefix(raw, " ") {
			cur.extra = append(cur.extra, strings.TrimSpace(l))
			continue
		}
		// anything else (warnings, tips, config errors) is kept as-is.
		preamble = append(preamble, l)
	}

	total := 0
	for _, f := range files {
		total += len(f.offs)
	}
	if summary == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if reported := reportedOffenses(summary); reported != total {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	cops := map[string]int{}
	for _, f := range files {
		fmt.Fprintf(&b, "%s (%d)\n", f.name, len(f.offs))
		for _, g := range groupOffenses(f.offs) {
			b.WriteString("  " + strings.Join(g.pos, ","))
			b.WriteString(" " + g.sev)
			if g.tag != "" {
				b.WriteString(" " + tagWord(g.tag))
			}
			b.WriteString(" " + g.cop + ": " + g.msg + "\n")
			for _, e := range g.extra {
				b.WriteString("    " + e + "\n")
			}
		}
		for _, o := range f.offs {
			cops[o.cop]++
		}
	}
	if h := histogram(cops); h != "" {
		b.WriteString("cops: " + h + "\n")
	}
	for _, l := range preamble {
		b.WriteString(l + "\n")
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

var offensesCountRE = regexp.MustCompile(`, (\d+) offenses? detected`)

func reportedOffenses(summary string) int {
	if strings.Contains(summary, "no offenses detected") {
		return 0
	}
	m := offensesCountRE.FindStringSubmatch(summary)
	if m == nil {
		return -1
	}
	var n int
	fmt.Sscan(m[1], &n)
	return n
}

func tagWord(tag string) string {
	switch tag {
	case "Correctable":
		return "fixable"
	case "Corrected":
		return "fixed"
	}
	return strings.ToLower(tag)
}

// groupOffenses folds offenses sharing severity, tag, cop and message into one
// group listing every position, keeping groups in order of first appearance.
func groupOffenses(offs []offense) []*group {
	var out []*group
	idx := map[string]*group{}
	for _, o := range offs {
		key := o.sev + "\x00" + o.tag + "\x00" + o.cop + "\x00" + o.msg + "\x00" + strings.Join(o.extra, "\n")
		g := idx[key]
		if g == nil {
			g = &group{sev: o.sev, tag: o.tag, cop: o.cop, msg: o.msg, extra: o.extra, firstLine: o.line}
			idx[key] = g
			out = append(out, g)
		}
		g.pos = append(g.pos, fmt.Sprintf("%d:%d", o.line, o.col))
	}
	return out
}

// histogram renders "Cop×N, ..." for cops reported more than once (at most 8,
// ordered by count then name) plus a count of the single-offense cops, which
// are already named in the per-file lines.
func histogram(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	singles := 0
	for k, v := range m {
		if v < 2 {
			singles++
			continue
		}
		kvs = append(kvs, kv{k, v})
	}
	if len(kvs) == 0 {
		return ""
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	var parts []string
	for i, e := range kvs {
		if i == 8 {
			singles += len(kvs) - 8
			break
		}
		parts = append(parts, fmt.Sprintf("%s×%d", e.k, e.v))
	}
	if singles > 0 {
		parts = append(parts, fmt.Sprintf("…+%d more cops", singles))
	}
	return strings.Join(parts, ", ")
}
