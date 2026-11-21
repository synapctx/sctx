package pyright

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	tyHeaderRe  = regexp.MustCompile(`^(error|warning|info)\[([\w-]+)\]: (.*)$`)
	tyArrowRe   = regexp.MustCompile(`^\s*--> (.+?):(\d+):(\d+)$`)
	tyNoteRe    = regexp.MustCompile(`^(info|help|note): (.*)$`)
	tyAnnotRe   = regexp.MustCompile(`^\s+\|(?:\s+(.*))?$`)
	tyMarkerRun = regexp.MustCompile(`[\^\-~|]+`)
	tySummaryRe = regexp.MustCompile(`^(Found \d+ diagnostics?|All checks passed!)\s*$`)
)

// tyLabels extracts the label text from a snippet annotation line: the
// "|   ---   ^^^^ Incompatible value" shape yields "Incompatible value".
func tyLabels(rest string) string {
	return strings.TrimSpace(tyMarkerRun.ReplaceAllString(rest, " "))
}

// renderTy re-renders ty's rustc-style report: one line per diagnostic with
// its location, the primary message, and the snippet's label text; related
// "info:" locations follow as indented notes.
func renderTy(stdout []byte, detailCap int) (format.Rendered, error) {
	lines := splitLines(stdout)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	summary := ""
	n := 0
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if tySummaryRe.MatchString(l) {
			summary = strings.TrimSpace(l)
			continue
		}
		h := tyHeaderRe.FindStringSubmatch(l)
		if h == nil {
			if strings.TrimSpace(l) == "" {
				continue
			}
			// Anything outside a diagnostic block is kept verbatim.
			b.WriteString(l)
			b.WriteByte('\n')
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "" {
			j++
		}
		block := lines[i+1 : j]
		i = j - 1
		n++
		var loc string
		var labels, notes []string
		cur := &labels
		for _, bl := range block {
			if m := tyArrowRe.FindStringSubmatch(bl); m != nil {
				if loc == "" {
					loc = fmt.Sprintf("%s:%s:%s", m[1], m[2], m[3])
				} else if len(notes) > 0 {
					notes[len(notes)-1] += fmt.Sprintf(" (%s:%s:%s)", m[1], m[2], m[3])
				}
				continue
			}
			if m := tyNoteRe.FindStringSubmatch(bl); m != nil {
				notes = append(notes, m[1]+": "+m[2])
				cur = &labels
				continue
			}
			if m := tyAnnotRe.FindStringSubmatch(bl); m != nil && strings.TrimSpace(bl) != "|" {
				if t := tyLabels(m[1]); t != "" {
					*cur = append(*cur, t)
				}
			}
		}
		if loc == "" {
			// No location: not the shape we know; show the block as is.
			b.WriteString(l)
			b.WriteByte('\n')
			for _, bl := range block {
				b.WriteString(bl)
				b.WriteByte('\n')
			}
			continue
		}
		fmt.Fprintf(&b, "%s %s[%s]: %s", loc, h[1], h[2], h[3])
		if len(labels) > 0 {
			fmt.Fprintf(&b, " — %s", strings.Join(labels, "; "))
		}
		b.WriteByte('\n')
		for _, nt := range notes {
			fmt.Fprintf(&b, "  %s\n", nt)
		}
	}
	if summary == "" || n == 0 && summary != "All checks passed!" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	b.WriteString(summary)
	return format.Rendered{Body: []byte(b.String()), Elided: n > 0}, nil
}
