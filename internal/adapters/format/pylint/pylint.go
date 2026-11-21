// Package pylint implements format.Formatters for the Python linters pylint
// and flake8. Both print one "path:line:col: CODE message" line per finding;
// linters on a legacy codebase repeat the same finding hundreds of times, so
// identical (code, message) findings within one file collapse to one line
// that lists every location. pylint's module banners, rule-symbol suffixes
// and blank/rule lines drop; its rating line is kept.
package pylint

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type tool int

const (
	toolPylint tool = iota
	toolFlake8
)

// Formatter renders pylint or flake8 output.
type Formatter struct {
	command string
	tool    tool
}

// New constructs the pylint formatter.
func New() *Formatter { return &Formatter{command: "pylint", tool: toolPylint} }

// NewFlake8 constructs the flake8 formatter.
func NewFlake8() *Formatter { return &Formatter{command: "flake8", tool: toolFlake8} }

// All returns every formatter this package provides, for registration.
func All() []*Formatter { return []*Formatter{New(), NewFlake8()} }

// Descriptor claims every invocation of the formatter's program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

// Aggressive renders findings grouped per file.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in)
}

// Relaxed shares the aggressive rendering: the line grammar is fully known,
// there is no weaker heuristic to fall back to.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in)
}

var (
	diagRe   = regexp.MustCompile(`^(.+?):(\d+):(\d+): ([A-Z]\d{1,4}):? (.*)$`)
	symbolRe = regexp.MustCompile(` \([a-z][a-z0-9-]*(?:, [a-z][a-z0-9-]*)*\)$`)
	moduleRe = regexp.MustCompile(`^\*{5,} Module .+$`)
	ruleRe   = regexp.MustCompile(`^-{10,}$`)
)

// userShaped reports flags that change the report format.
func userShaped(argv []string) bool {
	for _, a := range argv[1:] {
		switch {
		case a == "--version", a == "--help", a == "-h", a == "--list-msgs", a == "--generate-rcfile",
			a == "--help-msg", strings.HasPrefix(a, "--reports"), strings.HasPrefix(a, "--msg-template"),
			strings.HasPrefix(a, "--format"), strings.HasPrefix(a, "--output-format"),
			a == "-f":
			return true
		case a == "--statistics" || a == "--count" || a == "-q" || a == "--quiet" || a == "--benchmark":
			// Adds lines the grouping would drop or misread; keep the report as is.
			return true
		}
	}
	return false
}

type finding struct {
	line, col, code, msg string
}

type group struct {
	code, msg string
	locs      []string
}

func (f *Formatter) render(in format.Input) (format.Rendered, error) {
	if userShaped(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var out, errb []byte
	if in.Stdout != nil {
		out, _ = io.ReadAll(in.Stdout)
	}
	if in.Stderr != nil {
		errb, _ = io.ReadAll(in.Stderr)
	}
	_ = errb // stderr is left on the real stream
	text := strings.TrimRight(string(out), "\r\n")
	if text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var order []string
	byFile := map[string][]finding{}
	var other []string
	for _, l := range strings.Split(text, "\n") {
		if m := diagRe.FindStringSubmatch(l); m != nil {
			msg := m[5]
			if f.tool == toolPylint {
				msg = symbolRe.ReplaceAllString(msg, "")
			}
			if _, ok := byFile[m[1]]; !ok {
				order = append(order, m[1])
			}
			byFile[m[1]] = append(byFile[m[1]], finding{m[2], m[3], m[4], msg})
			continue
		}
		if strings.TrimSpace(l) == "" || moduleRe.MatchString(l) || ruleRe.MatchString(l) {
			continue
		}
		other = append(other, l)
	}
	if len(order) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	total := 0
	for _, file := range order {
		fs := byFile[file]
		total += len(fs)
		fmt.Fprintf(&b, "%s (%d)\n", file, len(fs))
		var groups []*group
		idx := map[string]*group{}
		for _, x := range fs {
			k := x.code + "\x00" + x.msg
			g := idx[k]
			if g == nil {
				g = &group{code: x.code, msg: x.msg}
				idx[k] = g
				groups = append(groups, g)
			}
			g.locs = append(g.locs, x.line+":"+x.col)
		}
		for _, g := range groups {
			if len(g.locs) == 1 {
				fmt.Fprintf(&b, "  L%s %s %s\n", g.locs[0], g.code, g.msg)
				continue
			}
			fmt.Fprintf(&b, "  %s %s ×%d @ %s\n", g.code, g.msg, len(g.locs), strings.Join(g.locs, " "))
		}
	}
	for _, l := range other {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(order) > 1 {
		fmt.Fprintf(&b, "%d findings in %d files", total, len(order))
	}
	return format.Rendered{Body: []byte(strings.TrimRight(b.String(), "\n"))}, nil
}
