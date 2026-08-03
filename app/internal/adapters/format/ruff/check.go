package ruff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// diagnosticRe matches ruff check's default diagnostic line:
// "path/to/file.py:12:5: F401 [*] `os` imported but unused" — the "[*] "
// marker is present only when the violation is auto-fixable.
var diagnosticRe = regexp.MustCompile(`^(\S+):(\d+):(\d+): ([A-Z]+\d+) (\[\*\] )?(.+)$`)

// foundRe matches the trailing summary ruff check prints, e.g.
// "Found 3 errors." or "Found 3 errors (1 fixable with the `--fix` option).".
var foundRe = regexp.MustCompile(`^Found (\d+) errors?\b`)

// fixableHintRe matches the standalone fixable-count hint ruff check prints
// as its own line when the combined "Found N errors (M fixable...)" form is
// not used.
var fixableHintRe = regexp.MustCompile(`^\[\*\] (\d+) fixable with the .+ option\.$`)

type diagnostic struct {
	file, line, col, code, message string
	fixable                        bool
	// contextLines counts non-diagnostic lines ruff prints beneath this
	// diagnostic (e.g. a source snippet), so they can be dropped in favor of
	// a single per-diagnostic marker rather than duplicated verbatim.
	contextLines int
}

// aggressiveCheck parses `ruff check` output into a per-file grouped
// listing: one summary line per file, one line per diagnostic, preserving
// every diagnostic while collapsing source-context noise and the trailing
// fixable-count hint into explicit markers.
func aggressiveCheck(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)

	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var diags []diagnostic
	var order []string
	seen := map[string]bool{}
	foundLine := ""
	fixableHint := -1
	cleanPass := false

	for _, line := range lines {
		if m := diagnosticRe.FindStringSubmatch(line); m != nil {
			diags = append(diags, diagnostic{
				file: m[1], line: m[2], col: m[3], code: m[4],
				fixable: m[5] != "", message: m[6],
			})
			if !seen[m[1]] {
				seen[m[1]] = true
				order = append(order, m[1])
			}
			continue
		}
		if m := foundRe.FindStringSubmatch(line); m != nil {
			foundLine = line
			continue
		}
		if m := fixableHintRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			fixableHint = n
			continue
		}
		if strings.TrimSpace(line) == "All checks passed!" {
			cleanPass = true
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		// A non-diagnostic, non-blank line following a diagnostic is
		// source-context ruff prints beneath it; attribute it to the most
		// recent diagnostic.
		if len(diags) > 0 {
			diags[len(diags)-1].contextLines++
		}
	}

	if len(diags) == 0 {
		if cleanPass {
			return format.Rendered{Body: []byte("All checks passed!")}, nil
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}

	byFile := map[string][]diagnostic{}
	for _, d := range diags {
		byFile[d.file] = append(byFile[d.file], d)
	}

	fixableCount := 0
	var b strings.Builder
	for _, file := range order {
		fmt.Fprintf(&b, "%s — %d issues\n", file, len(byFile[file]))
		for _, d := range byFile[file] {
			if d.fixable {
				fixableCount++
			}
			fmt.Fprintf(&b, "  L%s:%s %s %s", d.line, d.col, d.code, d.message)
			if d.fixable {
				b.WriteString(" [*]")
			}
			if d.contextLines > 1 {
				fmt.Fprintf(&b, " …+%d context", d.contextLines)
			}
			b.WriteByte('\n')
		}
	}

	fmt.Fprintf(&b, "%d issues in %d files", len(diags), len(order))
	if fixableHint >= 0 {
		fixableCount = fixableHint
	}
	if fixableCount > 0 {
		fmt.Fprintf(&b, " (%d fixable with --fix)", fixableCount)
	}
	_ = foundLine // summary count is redundant with the computed totals above

	return format.Rendered{Body: []byte(b.String())}, nil
}
