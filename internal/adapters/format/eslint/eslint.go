// Package eslint implements a format.Formatter for ESLint's default "stylish"
// output. Problems are grouped per file with a rule histogram; every error is
// kept, while warnings beyond a per-file cap are counted behind a marker.
//
// Biome and oxlint are deliberately not claimed: their default reporters are
// their own multi-line diagnostic formats, not stylish.
package eslint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

// warnCap is the warnings kept per file; errors are never capped.
const warnCap = 5

var (
	problemRE = regexp.MustCompile(`^\s+(\d+):(\d+)\s+(error|warning)\s+(.*)$`)
	ruleRE    = regexp.MustCompile(`^[@a-z0-9][\w@/.-]*$`)
	summaryRE = regexp.MustCompile(`^✖ (\d+) problems? \((\d+) errors?, (\d+) warnings?\)$`)
	fixableRE = regexp.MustCompile(`^\s+\d+ errors? and \d+ warnings? potentially fixable`)
)

// Formatter renders eslint output.
type Formatter struct{}

// New constructs an eslint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every eslint invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "eslint"} }

// Aggressive parses stylish output. Another explicit -f/--format makes the
// output someone else's grammar, so the tier steps aside.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return Render(in)
}

// Relaxed collapses provably redundant runs only.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !stylishRequested(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := jsutil.Streams(in)
	outLines, c1 := collapse.Runs(collapse.SplitLines(stdout))
	errLines, c2 := collapse.Runs(collapse.SplitLines(stderr))
	if !c1 && !c2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append(append([]string(nil), outLines...), errLines...)
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0}, nil
}

// stylishRequested is false when argv names a non-stylish formatter.
func stylishRequested(argv []string) bool {
	for i, a := range argv {
		var v string
		switch {
		case (a == "-f" || a == "--format") && i+1 < len(argv):
			v = argv[i+1]
		case strings.HasPrefix(a, "--format="):
			v = strings.TrimPrefix(a, "--format=")
		default:
			continue
		}
		return v == "stylish"
	}
	return true
}

type problem struct {
	line, col, rule, msg string
	warn                 bool
}

// Render is the aggressive tier over a captured invocation, exported so the
// npx/npm formatters can delegate when they recognise stylish output.
func Render(in format.Input) (format.Rendered, error) {
	if !stylishRequested(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := jsutil.Streams(in)
	var (
		files   []string
		byFile  = map[string][]problem{}
		cur     string
		pending string
		haveCur bool
		other   []string
		fixable string
		summary []int
		total   int
	)
	for _, l := range jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...)) {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := problemRE.FindStringSubmatch(l); m != nil && (haveCur || pending != "") {
			if pending != "" {
				cur, haveCur, pending = pending, true, ""
				if _, ok := byFile[cur]; !ok {
					files = append(files, cur)
				}
			}
			pr := problem{line: m[1], col: m[2], warn: m[3] == "warning"}
			pr.msg, pr.rule = splitRule(m[4])
			byFile[cur] = append(byFile[cur], pr)
			total++
			continue
		}
		if m := summaryRE.FindStringSubmatch(l); m != nil {
			for _, s := range m[1:] {
				n, _ := strconv.Atoi(s)
				summary = append(summary, n)
			}
			continue
		}
		if fixableRE.MatchString(l) {
			fixable = strings.TrimSpace(l)
			continue
		}
		if l[0] != ' ' && l[0] != '\t' && !strings.HasPrefix(l, "✖") && !strings.Contains(l, "  ") {
			// A candidate file header; it only counts once a problem follows.
			if pending != "" {
				other = append(other, pending)
			}
			pending = l
			continue
		}
		other = append(other, l)
	}
	if pending != "" {
		other = append(other, pending)
	}
	if total == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(summary) == 3 && summary[0] != total {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return renderAll(files, byFile, other, fixable, len(stderr) > 0), nil
}

// splitRule separates the trailing rule id (two or more spaces before it)
// from the message. Parser errors carry no rule.
func splitRule(s string) (msg, rule string) {
	i := strings.LastIndex(s, "  ")
	if i < 0 {
		return strings.TrimSpace(s), ""
	}
	if r := strings.TrimSpace(s[i:]); ruleRE.MatchString(r) {
		return strings.TrimSpace(s[:i]), r
	}
	return strings.TrimSpace(s), ""
}

func renderAll(files []string, byFile map[string][]problem, other []string, fixable string, fold bool) format.Rendered {
	var b strings.Builder
	rules := map[string]int{}
	errs, warns, nfiles := 0, 0, 0
	elided := false
	for _, file := range files {
		ps := byFile[file]
		if len(ps) == 0 {
			continue
		}
		nfiles++
		fe, fw := 0, 0
		for _, p := range ps {
			if p.warn {
				fw++
			} else {
				fe++
			}
		}
		errs, warns = errs+fe, warns+fw
		fmt.Fprintf(&b, "%s — %s\n", file, counts(fe, fw))
		shown, hidden := 0, 0
		for _, p := range ps {
			if p.rule != "" {
				rules[p.rule]++
			}
			if p.warn {
				if shown >= warnCap {
					hidden++
					continue
				}
				shown++
			}
			writeProblem(&b, p)
		}
		if hidden > 0 {
			fmt.Fprintf(&b, "  …+%d more warnings in file\n", hidden)
			elided = true
		}
	}
	fmt.Fprintf(&b, "%s in %d %s", counts(errs, warns), nfiles, jsutil.Plural(nfiles, "file", "files"))
	if len(rules) > 0 {
		b.WriteString("\n" + jsutil.Histogram("rules", rules))
	}
	if fixable != "" {
		b.WriteString("\n" + fixable)
	}
	if len(other) > 0 {
		b.WriteString("\nother output:")
		for _, l := range other {
			b.WriteString("\n" + l)
		}
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: elided}
}

func counts(errs, warns int) string {
	var parts []string
	if errs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", errs, jsutil.Plural(errs, "error", "errors")))
	}
	if warns > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", warns, jsutil.Plural(warns, "warning", "warnings")))
	}
	return strings.Join(parts, ", ")
}

func writeProblem(b *strings.Builder, p problem) {
	fmt.Fprintf(b, "  L%s:%s ", p.line, p.col)
	if p.warn {
		b.WriteString("warn ")
	}
	if p.rule != "" {
		fmt.Fprintf(b, "%s: ", p.rule)
	}
	b.WriteString(p.msg + "\n")
}
