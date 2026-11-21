// Package gitleaks implements a format.Formatter for gitleaks' verbose text
// report (`gitleaks detect|git|dir -v`). Each leak becomes one line carrying
// its rule, file:line and commit. The secret value is never emitted: the
// Finding/Secret/Match lines (which carry the credential unless --redact was
// given, and partially even then) are dropped and counted, together with
// entropy, author, email, date and fingerprint.
package gitleaks

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders gitleaks output.
type Formatter struct{}

// New constructs a gitleaks Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every gitleaks invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "gitleaks"} }

var keyRE = regexp.MustCompile(`^(Finding|Secret|Match|RuleID|Entropy|File|Symlink|Line|StartLine|EndLine|Commit|Author|Email|Date|Message|Tags|Fingerprint|Link|Description):\s*(.*)$`)

type leak struct {
	rule, file, commit string
	line               int
}

var stderrRE = regexp.MustCompile(`^\d{1,2}:\d{2}(?:AM|PM)\s+(\w+)\s+(.*)$`)

// Aggressive parses the verbose report. gitleaks exits 1 when leaks were
// found, so recognition of the report decides applicability, not the code.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if v, ok := lintkit.OptionValue(in.Argv, "-f", "--report-format"); ok && v != "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if v, ok := lintkit.OptionValue(in.Argv, "-r", "--report-path"); ok && (v == "-" || v == "/dev/stdout") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	errRaw := lintkit.ReadAll(in.Stderr)
	var leaks []leak
	var cur *leak
	withheld := 0
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		m := keyRE.FindStringSubmatch(l)
		if m == nil {
			if strings.TrimSpace(l) == "" || cur != nil {
				continue // blank separator, or continuation of a multi-line Finding/Secret value
			}
			return format.Rendered{}, format.ErrTierInapplicable
		}
		if m[1] == "Finding" {
			leaks = append(leaks, leak{})
			cur = &leaks[len(leaks)-1]
		}
		if cur == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		switch m[1] {
		case "RuleID":
			cur.rule = m[2]
		case "File":
			cur.file = m[2]
		case "Line", "StartLine":
			cur.line, _ = strconv.Atoi(m[2])
		case "Commit":
			cur.commit = m[2]
			if len(cur.commit) > 8 {
				cur.commit = cur.commit[:8]
			}
		default:
			withheld++
		}
	}
	if len(leaks) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for _, l := range leaks {
		if l.rule == "" || l.file == "" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	var b strings.Builder
	hist := map[string]int{}
	var files []string
	by := map[string][]leak{}
	for _, l := range leaks {
		hist[l.rule]++
		if _, ok := by[l.file]; !ok {
			files = append(files, l.file)
		}
		by[l.file] = append(by[l.file], l)
	}
	fmt.Fprintf(&b, "gitleaks: %d leaks in %d files (%s)", len(leaks), len(files), lintkit.Histogram(hist))
	for _, file := range files {
		fmt.Fprintf(&b, "\n%s (%d)", file, len(by[file]))
		for _, l := range by[file] {
			fmt.Fprintf(&b, "\n  %s %s", lintkit.Pos(l.line, 0), l.rule)
			if l.commit != "" {
				b.WriteString(" " + l.commit)
			}
		}
	}
	fmt.Fprintf(&b, "\n…+%d lines withheld (secret/match values, entropy, author, email, date, fingerprint)", withheld)
	for _, l := range lintkit.Lines(errRaw) {
		l = lintkit.StripANSI(l)
		if m := stderrRE.FindStringSubmatch(l); m != nil {
			l = m[1] + " " + m[2]
		}
		if strings.TrimSpace(l) != "" {
			b.WriteString("\n" + l)
		}
	}
	body := b.String()
	if len(body) >= len(raw)+len(errRaw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true,
		Note: fmt.Sprintf("%d leaks", len(leaks))}, nil
}

// Relaxed declines: the aggressive tier models the whole report.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
