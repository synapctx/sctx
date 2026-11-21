package ruff

import (
	"fmt"
	"regexp"
	"sort"
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
var fixableHintRe = regexp.MustCompile(`^\[\*\] (\d+) fixable with (?:the )?.+?(?: option)?\.$`)

type diagnostic struct {
	file, line, col, code, message string
	fixable                        bool
	// contextLines counts non-diagnostic lines ruff prints beneath this
	// diagnostic (e.g. a source snippet), so they can be dropped in favor of
	// a single per-diagnostic marker rather than duplicated verbatim.
	contextLines int
}

// snippetRe matches the frame lines of the legacy one-line-header diagnostic
// format's source snippet: "  |", "12 | code", "   ^^^".
var snippetRe = regexp.MustCompile(`^\s*(\d+\s*)?\||^\s*\^`)

// minHistogramDiags is the diagnostic count from which a per-rule histogram
// line is added; below it the listing itself is already the summary.
const minHistogramDiags = 15

// aggressiveCheck parses `ruff check` output (full or concise format) into a
// per-file grouped listing: one summary line per file, one line per
// diagnostic, preserving every diagnostic and any line it does not
// recognise, while collapsing source snippets and the trailing fixable hint.
func aggressiveCheck(in format.Input) (format.Rendered, error) {
	if userShapedOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := splitLines(raw)

	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var diags []diagnostic
	var order []string
	seen := map[string]bool{}
	var extra []string
	foundLine := ""
	fixableHint := -1
	cleanPass := false
	snippets := false

	add := func(d diagnostic) {
		diags = append(diags, d)
		if !seen[d.file] {
			seen[d.file] = true
			order = append(order, d.file)
		}
	}

	for _, it := range parseItems(lines) {
		if it.isBlock {
			add(diagnostic{file: it.file, line: it.line, col: it.col, code: it.code,
				fixable: it.fixable, message: it.msg, contextLines: it.dropped})
			snippets = snippets || it.dropped > 0
			continue
		}
		line := it.plain
		if m := diagnosticRe.FindStringSubmatch(line); m != nil {
			add(diagnostic{file: m[1], line: m[2], col: m[3], code: m[4],
				fixable: m[5] != "", message: m[6]})
			continue
		}
		if foundRe.MatchString(line) {
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
		if len(diags) > 0 && snippetRe.MatchString(line) {
			diags[len(diags)-1].contextLines++
			continue
		}
		extra = append(extra, line)
	}

	if len(diags) == 0 {
		switch {
		case cleanPass && len(extra) == 0:
			return format.Rendered{Body: []byte("All checks passed!")}, nil
		case foundLine != "" && len(extra) == 0:
			return format.Rendered{Body: []byte(foundLine)}, nil
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
			if d.contextLines > 1 && !snippets {
				fmt.Fprintf(&b, " …+%d context", d.contextLines)
			}
			b.WriteByte('\n')
		}
	}
	for _, l := range extra {
		b.WriteString(l)
		b.WriteByte('\n')
	}

	if h := histogram(diags); len(diags) >= minHistogramDiags && h != "" {
		b.WriteString(h)
		b.WriteByte('\n')
	}

	if strings.Contains(foundLine, "fixed") {
		// "Found N errors (X fixed, Y remaining)." — the --fix outcome is
		// not derivable from the listing, keep it verbatim.
		fmt.Fprintf(&b, "%d issues in %d files — %s", len(diags), len(order), foundLine)
	} else {
		fmt.Fprintf(&b, "%d issues in %d files", len(diags), len(order))
		if fixableHint >= 0 {
			fixableCount = fixableHint
		}
		if fixableCount > 0 {
			fmt.Fprintf(&b, " (%d fixable with --fix)", fixableCount)
		}
	}
	if snippets {
		b.WriteString(" [source snippets omitted]")
	}

	return format.Rendered{Body: []byte(b.String()), Elided: snippets}, nil
}

// histogram renders "by rule: F401×5 I001×5 …" in descending count order
// (ties by code), covering every diagnostic.
func histogram(diags []diagnostic) string {
	counts := map[string]int{}
	for _, d := range diags {
		counts[d.code]++
	}
	if len(counts) < 3 {
		return ""
	}
	codes := make([]string, 0, len(counts))
	for c := range counts {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool {
		if counts[codes[i]] != counts[codes[j]] {
			return counts[codes[i]] > counts[codes[j]]
		}
		return codes[i] < codes[j]
	})
	var b strings.Builder
	b.WriteString("by rule:")
	for _, c := range codes {
		fmt.Fprintf(&b, " %s×%d", c, counts[c])
	}
	return b.String()
}
