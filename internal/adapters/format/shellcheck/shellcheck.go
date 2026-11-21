// Package shellcheck implements a format.Formatter for shellcheck's default
// (tty) report. Findings are grouped per file with a code histogram; source
// excerpts, caret lines, "Did you mean" fix blocks and wiki links are dropped
// and counted.
package shellcheck

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	headerRE = regexp.MustCompile(`^In (.+) line (\d+):$`)
	markerRE = regexp.MustCompile(`^( *)\^\S* (SC\d{4}) \((error|warning|info|style)\): (.*)$`)
	wikiRE   = regexp.MustCompile(`^\s+https://www\.shellcheck\.net/wiki/SC\d{4}`)
)

// Formatter renders shellcheck output.
type Formatter struct{}

// New constructs a shellcheck Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every shellcheck invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "shellcheck"} }

// machineFormat reports a non-tty --format, which is a data contract.
func machineFormat(argv []string) bool {
	v, ok := lintkit.OptionValue(argv, "-f", "--format")
	return ok && v != "tty"
}

// Aggressive groups the tty report. shellcheck exits 1 when it finds issues,
// so recognition of the report, not the exit code, decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lintkit.ReadAll(in.Stderr)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	fs, fixes, excerpts, wiki, ok := parse(lintkit.Lines(raw))
	if !ok || len(fs) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := lintkit.Render("shellcheck", fs,
		lintkit.Dropped{Label: "fix suggestions", N: fixes},
		lintkit.Dropped{Label: "source excerpts", N: excerpts},
		lintkit.Dropped{Label: "wiki links", N: wiki})
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:   []byte(body),
		Note:   fmt.Sprintf("%d findings", len(fs)),
		Elided: fixes > 0,
	}, nil
}

// Relaxed only drops the wiki-link footer and marks the elision.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	if machineFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(lintkit.ReadAll(in.Stderr)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	var out []string
	n := 0
	for _, l := range lintkit.Lines(raw) {
		if wikiRE.MatchString(l) {
			n++
			continue
		}
		out = append(out, l)
	}
	if n == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out = append(out, fmt.Sprintf("…+%d wiki links", n))
	return format.Rendered{Body: []byte(strings.Join(out, "\n"))}, nil
}

// parse reads the tty report. ok is false when any line is not understood, so
// an unknown shape can never silently lose findings.
func parse(lines []string) (fs []lintkit.Finding, fixes, excerpts, wiki int, ok bool) {
	file, line := "", 0
	inFix, inFooter, haveHeader := false, false, false
	for i := 0; i < len(lines); i++ {
		l := lintkit.StripANSI(lines[i])
		if inFooter {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if !wikiRE.MatchString(l) {
				return nil, 0, 0, 0, false
			}
			wiki++
			continue
		}
		if m := headerRE.FindStringSubmatch(l); m != nil {
			file = m[1]
			line, _ = strconv.Atoi(m[2])
			haveHeader, inFix = true, false
			excerpts++
			i++ // the source line itself
			if i >= len(lines) {
				return nil, 0, 0, 0, false
			}
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l == "For more information:" {
			inFooter = true
			continue
		}
		if inFix {
			continue
		}
		if l == "Did you mean:" {
			inFix = true
			fixes++
			continue
		}
		if m := markerRE.FindStringSubmatch(l); m != nil && haveHeader {
			fs = append(fs, lintkit.Finding{File: file, Line: line, Col: len(m[1]) + 1, Sev: m[3], Code: m[2], Msg: m[4]})
			continue
		}
		return nil, 0, 0, 0, false
	}
	return fs, fixes, excerpts, wiki, true
}

// Condense renders a shellcheck tty report embedded in another tool's output
// (for example a pre-commit hook). ok is false when the text is not a
// complete shellcheck report; the caller must then keep the text as it was.
func Condense(lines []string) (body string, ok bool) {
	fs, fixes, excerpts, wiki, ok := parse(lines)
	if !ok || len(fs) == 0 {
		return "", false
	}
	return lintkit.Render("shellcheck", fs,
		lintkit.Dropped{Label: "fix suggestions", N: fixes},
		lintkit.Dropped{Label: "source excerpts", N: excerpts},
		lintkit.Dropped{Label: "wiki links", N: wiki}), true
}
