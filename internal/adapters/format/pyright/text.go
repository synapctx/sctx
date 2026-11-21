package pyright

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxDetailLines caps the continuation lines kept per diagnostic; union and
// protocol mismatches can run to dozens of lines of the same shape.
const maxDetailLines = 6

var (
	// diagRe matches "  path:12:3 - error: message" (pyright, basedpyright).
	diagRe = regexp.MustCompile(`^  (.+?):(\d+):(\d+) - (error|warning|information|note): (.*)$`)
	// detailRe matches a continuation line: pyright indents with two spaces
	// followed by two NO-BREAK SPACEs (U+00A0), not four spaces.
	detailRe = regexp.MustCompile(`^  [ \x{a0}]{2}`)
	// summaryRe matches "5 errors, 16 warnings, 0 informations" / "…, 0 notes".
	summaryRe = regexp.MustCompile(`^\d+ errors?, \d+ warnings?, \d+ (?:informations?|notes?)\s*$`)
)

type textDiag struct {
	file, line, col, sev, msg string
	detail                    []string
}

// renderPyright re-renders pyright/basedpyright's report. Lines it does not
// recognise (version notices, config messages) are kept verbatim ahead of the
// diagnostics so nothing unexpected is hidden.
func renderPyright(stdout, stderr []byte, detailCap int) (format.Rendered, error) {
	lines := splitLines(stdout)
	if len(lines) == 0 || strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var (
		diags   []*textDiag
		order   []string
		seen    = map[string]bool{}
		extra   []string
		summary string
	)
	for i, l := range lines {
		if m := diagRe.FindStringSubmatch(l); m != nil {
			d := &textDiag{file: relPath(m[1]), line: m[2], col: m[3], sev: m[4], msg: m[5]}
			diags = append(diags, d)
			if !seen[d.file] {
				seen[d.file] = true
				order = append(order, d.file)
			}
			continue
		}
		if summaryRe.MatchString(l) {
			summary = strings.TrimSpace(l)
			continue
		}
		if detailRe.MatchString(l) && len(diags) > 0 {
			d := diags[len(diags)-1]
			d.detail = append(d.detail, strings.TrimSpace(l))
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		// A file header: a bare path directly above its first diagnostic.
		if !strings.HasPrefix(l, " ") && i+1 < len(lines) {
			if m := diagRe.FindStringSubmatch(lines[i+1]); m != nil && m[1] == l {
				continue
			}
		}
		extra = append(extra, l)
	}
	if summary == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for _, l := range extra {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	elided := false
	byFile := map[string][]*textDiag{}
	for _, d := range diags {
		byFile[d.file] = append(byFile[d.file], d)
	}
	for _, file := range order {
		fmt.Fprintf(&b, "%s (%d)\n", file, len(byFile[file]))
		for _, d := range byFile[file] {
			detail := d.detail
			more := 0
			if detailCap > 0 && len(detail) > detailCap {
				more = len(detail) - detailCap
				detail = detail[:detailCap]
				elided = true
			}
			// The rule name closes the LAST line of the message block.
			head := d.msg
			fmt.Fprintf(&b, "  L%s:%s %s: %s\n", d.line, d.col, d.sev, head)
			for _, x := range detail {
				fmt.Fprintf(&b, "    %s\n", x)
			}
			if more > 0 {
				fmt.Fprintf(&b, "    …+%d lines\n", more)
			}
		}
	}
	b.WriteString(summary)
	_ = stderr // stderr stays on the real stream (FoldStderr false)
	return format.Rendered{Body: []byte(b.String()), Elided: elided}, nil
}
