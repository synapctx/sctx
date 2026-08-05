package golangcilint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// issueRe matches golangci-lint's default issue line:
// "path/to/file.go:12:5: message here (linter)".
var issueRe = regexp.MustCompile(`^(\S+\.go):(\d+):(\d+): (.+?) \(([\w-]+)\)\s*$`)

type issue struct {
	file, line, col, message, linter string
	// contextLines counts source-context/caret lines golangci-lint prints
	// beneath this issue (the code snippet and the "^" pointer), so they can
	// be dropped in favor of a single per-issue marker.
	contextLines int
}

// aggressiveRun parses `golangci-lint run` output into a per-file grouped
// listing: one summary line per file, one line per issue, stripping the
// repeated path and any source-context lines golangci-lint prints beneath
// each issue. Every issue is preserved; only the context noise is elided,
// each elision carrying an explicit "...+N context" marker.
func aggressiveRun(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)

	var issues []issue
	var order []string
	seen := map[string]bool{}

	for _, line := range lines {
		if m := issueRe.FindStringSubmatch(line); m != nil {
			issues = append(issues, issue{file: m[1], line: m[2], col: m[3], message: m[4], linter: m[5]})
			if !seen[m[1]] {
				seen[m[1]] = true
				order = append(order, m[1])
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		// A non-issue, non-blank line following an issue is source-context
		// (code snippet or caret marker) that golangci-lint prints beneath
		// the issue; attribute it to the most recent issue.
		if len(issues) > 0 {
			issues[len(issues)-1].contextLines++
		}
	}

	if len(issues) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	byFile := map[string][]issue{}
	for _, is := range issues {
		byFile[is.file] = append(byFile[is.file], is)
	}

	var b strings.Builder
	for _, file := range order {
		fmt.Fprintf(&b, "%s — %d issues\n", file, len(byFile[file]))
		for _, is := range byFile[file] {
			fmt.Fprintf(&b, "  L%s:%s %s (%s)", is.line, is.col, is.message, is.linter)
			// A lone dropped context line needs no marker; only a run of two
			// or more (e.g. code snippet + caret) is worth calling out.
			if is.contextLines > 1 {
				fmt.Fprintf(&b, " …+%d context", is.contextLines)
			}
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "%d issues in %d files", len(issues), len(order))

	return format.Rendered{Body: []byte(b.String())}, nil
}
