package cargo

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	ansiRE    = regexp.MustCompile(`\x1b(\[[0-9;]*[A-Za-z]|\(B)`)
	fmtDiffRE = regexp.MustCompile(`^Diff in (.+?)(?::(\d+):| at line (\d+):)$`)
	fmtBudget = 60 // changed lines kept before hunks collapse to a count
)

type fmtHunk struct {
	loc     string
	changed []string
	ctx     int
}

// renderFmt compacts `cargo fmt --check`: colour stripped, context lines
// counted instead of repeated, and past a budget of changed lines a hunk is
// reduced to its location and size. The remedy (`cargo fmt`) is mechanical,
// so the location is what matters.
func renderFmt(stdout, stderr []byte) (format.Rendered, error) {
	text := ansiRE.ReplaceAllString(string(stdout), "")
	var hunks []*fmtHunk
	var cur *fmtHunk
	var rest []string
	for _, l := range splitLines(text) {
		if m := fmtDiffRE.FindStringSubmatch(l); m != nil {
			line := m[2]
			if line == "" {
				line = m[3]
			}
			cur = &fmtHunk{loc: m[1] + ":" + line}
			hunks = append(hunks, cur)
			continue
		}
		if cur == nil {
			rest = append(rest, l)
			continue
		}
		switch {
		case strings.HasPrefix(l, "-"), strings.HasPrefix(l, "+"):
			cur.changed = append(cur.changed, l)
		default:
			cur.ctx++
		}
	}
	if len(hunks) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	files := map[string]bool{}
	for _, h := range hunks {
		files[h.loc[:strings.LastIndex(h.loc, ":")]] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cargo fmt --check: %d hunk(s) in %d file(s) need formatting\n", len(hunks), len(files))
	used, ctx, collapsed := 0, 0, 0
	for _, h := range hunks {
		ctx += h.ctx
		b.WriteString(h.loc + "\n")
		if used+len(h.changed) > fmtBudget {
			add, del := 0, 0
			for _, c := range h.changed {
				if c[0] == '+' {
					add++
				} else {
					del++
				}
			}
			fmt.Fprintf(&b, "  +%d -%d lines\n", add, del)
			collapsed += len(h.changed)
			continue
		}
		used += len(h.changed)
		for _, c := range h.changed {
			b.WriteString(c + "\n")
		}
	}
	for _, l := range rest {
		b.WriteString(l + "\n")
	}
	for _, l := range splitLines(string(stderr)) {
		b.WriteString(l + "\n")
	}
	if ctx+collapsed > 0 {
		var parts []string
		if ctx > 0 {
			parts = append(parts, fmt.Sprintf("context ×%d", ctx))
		}
		if collapsed > 0 {
			parts = append(parts, fmt.Sprintf("collapsed hunk lines ×%d", collapsed))
		}
		fmt.Fprintf(&b, "…+%d lines (%s)\n", ctx+collapsed, strings.Join(parts, ", "))
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: true, Elided: ctx+collapsed > 0}, nil
}
