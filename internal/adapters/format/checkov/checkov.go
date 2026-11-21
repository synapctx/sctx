// Package checkov implements a format.Formatter for checkov's default CLI
// report. Failed checks are grouped per check id with every failing resource
// and its file:lines; PASSED checks, guide links and code excerpts are
// counted and dropped, and the banner and update notice are dropped. The
// per-framework totals line is kept.
package checkov

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders checkov output.
type Formatter struct{}

// New constructs a checkov Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every checkov invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "checkov"} }

func machineOutput(argv []string) bool {
	for _, name := range [][]string{{"-o", "--output"}} {
		for i := 1; i < len(argv); i++ {
			a := argv[i]
			if a == "--" {
				break
			}
			var v string
			switch {
			case a == name[0] || a == name[1]:
				if i+1 < len(argv) {
					v = argv[i+1]
				}
			case strings.HasPrefix(a, name[1]+"="):
				v = a[len(name[1])+1:]
			default:
				continue
			}
			if v != "cli" && v != "" {
				return true
			}
		}
	}
	return lintkit.HasFlag(argv, "--output-file-path", "--version", "-v", "--list", "-l", "--help", "-h")
}

var (
	sectionRE = regexp.MustCompile(`^(\w[\w ]*) scan results:$`)
	totalsRE  = regexp.MustCompile(`^Passed checks: \d+, Failed checks: \d+, Skipped checks: \d+`)
	checkRE   = regexp.MustCompile(`^Check: (\S+): "(.*)"$`)
	statusRE  = regexp.MustCompile(`^\t(PASSED|FAILED|SKIPPED) for resource: (.*)$`)
)

type fail struct{ resource, file string }

type check struct {
	id, desc string
	fails    []fail
}

type section struct {
	name    string
	totals  string
	order   []*check
	byID    map[string]*check
	passed  int
	skipped []string
}

// Aggressive parses the report. checkov exits 1 when checks fail, so
// recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	var secs []*section
	var sec *section
	var cur *check
	var curStatus, curRes string
	dropped, preamble := 0, 0
	var kept []string // pre-header lines that look like errors
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		switch {
		case sectionRE.MatchString(l):
			sec = &section{name: sectionRE.FindStringSubmatch(l)[1], byID: map[string]*check{}}
			secs = append(secs, sec)
			cur = nil
			continue
		case sec == nil:
			preamble++
			if low := strings.ToLower(l); strings.Contains(low, "error") || strings.Contains(low, "warning") {
				kept = append(kept, strings.TrimSpace(l))
			}
			continue
		case strings.TrimSpace(l) == "":
			continue
		case totalsRE.MatchString(l):
			sec.totals = l
			continue
		}
		if m := checkRE.FindStringSubmatch(l); m != nil {
			c := sec.byID[m[1]]
			if c == nil {
				c = &check{id: m[1], desc: m[2]}
				sec.byID[m[1]] = c
				sec.order = append(sec.order, c)
			}
			cur, curStatus, curRes = c, "", ""
			continue
		}
		if cur == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if m := statusRE.FindStringSubmatch(l); m != nil {
			curStatus, curRes = m[1], m[2]
			switch curStatus {
			case "PASSED":
				sec.passed++
			case "SKIPPED":
				sec.skipped = append(sec.skipped, cur.id+" "+curRes)
			case "FAILED":
				cur.fails = append(cur.fails, fail{resource: curRes})
			}
			continue
		}
		if strings.HasPrefix(l, "\tFile: ") {
			if curStatus == "FAILED" && len(cur.fails) > 0 {
				cur.fails[len(cur.fails)-1].file = strings.TrimPrefix(l, "\tFile: ")
			}
			continue
		}
		// Guide, Calling File, code excerpt, suppress comment.
		dropped++
	}
	if len(secs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	b.WriteString("checkov:")
	for _, s := range secs {
		fmt.Fprintf(&b, "\n%s: %s", s.name, strings.TrimSpace(s.totals))
		for _, c := range s.order {
			if len(c.fails) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n%s %s (%d)", c.id, lintkit.Clip(c.desc), len(c.fails))
			for _, f := range c.fails {
				b.WriteString("\n  " + f.resource)
				if f.file != "" {
					b.WriteString(" " + f.file)
				}
			}
		}
		for _, sk := range s.skipped {
			b.WriteString("\nSKIPPED " + sk)
		}
	}
	passed := 0
	for _, s := range secs {
		passed += s.passed
	}
	fmt.Fprintf(&b, "\n…+%d passed checks, %d guide/code/detail lines, banner (%d lines) dropped", passed, dropped, preamble)
	for _, k := range kept {
		b.WriteString("\n" + k)
	}
	for _, l := range lintkit.Lines(errRaw) {
		if t := strings.TrimSpace(lintkit.StripANSI(l)); t != "" {
			b.WriteString("\n" + t)
		}
	}
	body := b.String()
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true,
		Note: fmt.Sprintf("%d sections", len(secs))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
