// Package ansiblelint implements a format.Formatter for ansible-lint's
// default report (violations on stdout, summary on stderr). Violations are
// grouped per file with a rule histogram; the stderr "Rule Violation
// Summary" table (a re-count of the same findings) and the documentation
// pointer are counted and dropped, and the Failed/Passed totals line is kept.
package ansiblelint

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders ansible-lint output.
type Formatter struct{}

// New constructs an ansible-lint Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every ansible-lint invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "ansible-lint"} }

var (
	ruleRE = regexp.MustCompile(`^([a-z][a-z0-9-]*(?:\[[^\]]+\])?): (.*)$`)
	locRE  = regexp.MustCompile(`^(\S+?):(\d+)(?::(\d+))?(?: (.*))?$`)
	sumRE  = regexp.MustCompile(`^\s+\d+ [a-z][a-z0-9-]* profile:\S+ tags:\S+$`)
)

// Aggressive parses the report. ansible-lint exits 2 on violations, so
// recognition of the report decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if v, ok := lintkit.OptionValue(in.Argv, "-f", "--format"); ok && v != "rich" && v != "full" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if lintkit.HasFlag(in.Argv, "-p", "--parseable", "-q", "--version", "-L", "--list-rules", "-T", "--list-tags", "--help", "-h") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	var fs []lintkit.Finding
	lines := lintkit.Lines(raw)
	for i := 0; i < len(lines); i++ {
		l := lintkit.StripANSI(lines[i])
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := ruleRE.FindStringSubmatch(l)
		if m == nil || i+1 >= len(lines) {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		loc := locRE.FindStringSubmatch(lintkit.StripANSI(lines[i+1]))
		if loc == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		i++
		ln, _ := strconv.Atoi(loc[2])
		col, _ := strconv.Atoi(loc[3])
		msg := m[2]
		if loc[4] != "" {
			msg += " [" + loc[4] + "]"
		}
		fs = append(fs, lintkit.Finding{File: loc[1], Line: ln, Col: col, Code: m[1], Msg: msg})
	}
	if len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var keep []string
	dropped := 0
	for _, l := range lintkit.Lines(errRaw) {
		l = lintkit.StripANSI(l)
		t := strings.TrimSpace(l)
		switch {
		case t == "":
		case sumRE.MatchString(l), t == "# Rule Violation Summary", strings.HasPrefix(t, "Read documentation for instructions"):
			dropped++
		default:
			keep = append(keep, t)
		}
	}
	body := lintkit.Render("ansible-lint", fs, lintkit.Dropped{Label: "stderr summary lines", N: dropped})
	if len(keep) > 0 {
		body += "\n" + strings.Join(keep, "\n")
	}
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: dropped > 0,
		Note: fmt.Sprintf("%d findings", len(fs))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
