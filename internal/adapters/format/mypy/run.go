package mypy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// diagRe matches an error/warning diagnostic line:
// "path/to/file.py:12: error: message  [error-code]" or
// "path/to/file.py:12:5: error: message  [error-code]" (column optional,
// error-code optional).
// The file group uses a non-greedy \S+? so it stops at the first
// "file:line:" boundary rather than the rightmost one a greedy \S+ would
// find on lines whose message also contains "digit:" sequences.
var diagRe = regexp.MustCompile(`^(\S+?):(\d+):(?:(\d+):)? (error|warning): (.+?)(?:\s+\[([\w.-]+)\])?$`)

// noteRe matches a note line attached to the preceding diagnostic:
// "path/to/file.py:12: note: message".
var noteRe = regexp.MustCompile(`^\S+?:\d+:(?:\d+:)? note: (.+)$`)

// summaryRe matches mypy's terminal summary line, either the clean-run
// message or the error/warning count.
var summaryRe = regexp.MustCompile(`^(Success: no issues found in \d+ source files?|Found \d+ errors?(?:, \d+ warnings?)? in \d+ files?( \(checked \d+ source files?\))?.*)$`)

// maxNotesShown caps the notes rendered per diagnostic; notes are secondary
// to the error/warning line itself, so a long chain is collapsed behind an
// explicit marker.
const maxNotesShown = 2

type diagnostic struct {
	file, line, col, severity, message, code string
	notes                                    []string
}

// aggressiveReport parses mypy's default output into a per-file grouped
// listing: one summary line per file, one line per diagnostic, with note
// chains capped and elided behind an explicit "...+N notes" marker. Every
// diagnostic and the final summary are preserved.
func aggressiveReport(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)

	var diags []diagnostic
	var order []string
	seen := map[string]bool{}
	var summary string

	for _, line := range lines {
		if m := diagRe.FindStringSubmatch(line); m != nil {
			diags = append(diags, diagnostic{
				file:     m[1],
				line:     m[2],
				col:      m[3],
				severity: m[4],
				message:  m[5],
				code:     m[6],
			})
			if !seen[m[1]] {
				seen[m[1]] = true
				order = append(order, m[1])
			}
			continue
		}
		if m := noteRe.FindStringSubmatch(line); m != nil && len(diags) > 0 {
			diags[len(diags)-1].notes = append(diags[len(diags)-1].notes, m[1])
			continue
		}
		if summaryRe.MatchString(line) {
			summary = line
			continue
		}
		// Unrecognized non-blank line (e.g. continuation of a multiline
		// message): ignored — not counted as signal loss since it carries no
		// file:line diagnostic identity of its own.
	}

	if len(diags) == 0 && summary == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if len(diags) == 0 {
		// Clean run: "Success: no issues found in N source files".
		return format.Rendered{Body: []byte(summary)}, nil
	}

	byFile := map[string][]diagnostic{}
	for _, d := range diags {
		byFile[d.file] = append(byFile[d.file], d)
	}

	var b strings.Builder
	for _, file := range order {
		fmt.Fprintf(&b, "%s — %d diagnostics\n", file, len(byFile[file]))
		for _, d := range byFile[file] {
			loc := d.line
			if d.col != "" {
				loc = d.line + ":" + d.col
			}
			fmt.Fprintf(&b, "  L%s %s: %s", loc, d.severity, d.message)
			if d.code != "" {
				fmt.Fprintf(&b, " [%s]", d.code)
			}
			b.WriteByte('\n')

			shown := d.notes
			extra := 0
			if len(shown) > maxNotesShown {
				extra = len(shown) - maxNotesShown
				shown = shown[:maxNotesShown]
			}
			for _, n := range shown {
				fmt.Fprintf(&b, "    note: %s\n", n)
			}
			if extra > 0 {
				fmt.Fprintf(&b, "    …+%d notes\n", extra)
			}
		}
	}

	if summary != "" {
		b.WriteString(summary)
	} else {
		fmt.Fprintf(&b, "%d diagnostics in %d files", len(diags), len(order))
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}
