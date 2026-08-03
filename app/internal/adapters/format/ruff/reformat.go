package ruff

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// perFileRe matches the two per-file lines ruff format prints: "Reformatted
// path.py" when actually rewriting, and "Would reformat: path.py" under
// --check/--diff.
var perFileRe = regexp.MustCompile(`^(?:Reformatted|Would reformat:) (.+)$`)

// formatSummaryRe matches ruff format's trailing summary line, e.g.
// "2 files reformatted, 3 files left unchanged" or
// "2 files would be reformatted, 1 file already formatted".
var formatSummaryRe = regexp.MustCompile(`^\d+ files? (?:reformatted|would be reformatted|left unchanged|already formatted)\b`)

// maxPerFileLines caps how many per-file reformat lines are kept verbatim
// before collapsing the remainder into a "…+N" marker; the summary line
// always carries the true total regardless of the cap.
const maxPerFileLines = 10

// aggressiveFormat parses `ruff format` output into a capped per-file list
// plus the trailing summary. Every elided per-file line is accounted for by
// an explicit "…+N" marker; the summary count is never altered.
func aggressiveFormat(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)

	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var perFile []string
	var summary string

	for _, line := range lines {
		if m := perFileRe.FindStringSubmatch(line); m != nil {
			perFile = append(perFile, m[1])
			continue
		}
		if formatSummaryRe.MatchString(line) {
			summary = line
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Unrecognized non-blank content (e.g. a --diff hunk): not ruff
		// format's known shape, let a lower tier handle it.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if summary == "" && len(perFile) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	shown := perFile
	elided := 0
	if len(shown) > maxPerFileLines {
		elided = len(shown) - maxPerFileLines
		shown = shown[:maxPerFileLines]
	}
	for _, p := range shown {
		fmt.Fprintf(&b, "%s\n", p)
	}
	if elided > 0 {
		fmt.Fprintf(&b, "…+%d more\n", elided)
	}
	if summary != "" {
		b.WriteString(summary)
	} else {
		fmt.Fprintf(&b, "%d files reformatted", len(perFile))
	}

	return format.Rendered{Body: []byte(strings.TrimRight(b.String(), "\n"))}, nil
}
