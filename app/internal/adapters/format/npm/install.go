package npm

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxDeprecationWarnings caps how many deprecation-warning lines are kept
// verbatim before eliding the rest behind a "…+N more" marker.
const maxDeprecationWarnings = 3

// npmSummaryRe matches npm's terse install result line, e.g.
// "added 5 packages, and audited 10 packages in 2s" or
// "up to date, audited 3 packages in 500ms" or "removed 2 packages in 1s".
var npmSummaryRe = regexp.MustCompile(`^(added|removed|changed|up to date)\b`)

// isInstallSummaryLine reports whether t is one of the package managers'
// final result lines (npm's added/removed/changed/up to date, pnpm's
// "Packages:"/"Done in", yarn's "Done in"/"success ...").
func isInstallSummaryLine(t string) bool {
	if npmSummaryRe.MatchString(t) {
		return true
	}
	switch {
	case strings.HasPrefix(t, "Packages:"),
		strings.HasPrefix(t, "Done in"),
		strings.HasPrefix(t, "success "):
		return true
	default:
		return false
	}
}

// isInstallNoiseLine reports whether t is progress/diff noise from an
// install that carries no signal once a summary line exists: pnpm's package
// diff rows and resolve progress, spinner-style "Collecting"/"Downloading"
// equivalents.
func isInstallNoiseLine(t string) bool {
	switch {
	case strings.HasPrefix(t, "+ "), strings.HasPrefix(t, "- "):
		return true
	case strings.HasPrefix(t, "Progress:"):
		return true
	case strings.HasPrefix(t, "warning "):
		return true
	default:
		return false
	}
}

// aggressiveInstall collapses install/ci/i/add/update output, dominated by
// progress spinners, deprecation warnings, and funding nags, down to: the
// final summary line(s), a capped list of deprecation warnings, and a noise
// count. On failure the full npm/pnpm/yarn error block is kept verbatim.
func aggressiveInstall(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	outLines := splitLines(rawOut)
	errLines := splitLines(rawErr)
	if len(outLines) == 0 && len(errLines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var errorLines, summaryLines, deprecationLines []string
	fundingCount, noiseCount := 0, 0

	classify := func(l string) {
		t := strings.TrimSpace(l)
		if t == "" {
			return
		}
		switch {
		case isErrorLine(t):
			errorLines = append(errorLines, t)
		case isInstallSummaryLine(t):
			summaryLines = append(summaryLines, t)
		case isFundingLine(t):
			fundingCount++
		case isDeprecationLine(t):
			deprecationLines = append(deprecationLines, t)
		case isInstallNoiseLine(t):
			noiseCount++
		default:
			noiseCount++
		}
	}
	for _, l := range outLines {
		classify(l)
	}
	for _, l := range errLines {
		classify(l)
	}

	if in.ExitCode != 0 {
		if len(errorLines) == 0 {
			// No recognizable error signal; let relaxed filtering preserve
			// whatever stderr/stdout actually says.
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return format.Rendered{
			Body:       []byte(strings.Join(errorLines, "\n")),
			FoldStderr: len(rawErr) > 0,
		}, nil
	}

	var b strings.Builder
	if len(summaryLines) > 0 {
		b.WriteString(strings.Join(summaryLines, "\n"))
	} else {
		b.WriteString("install: no changes")
	}

	if len(deprecationLines) > 0 {
		shown, extra := deprecationLines, 0
		if len(deprecationLines) > maxDeprecationWarnings {
			shown, extra = deprecationLines[:maxDeprecationWarnings], len(deprecationLines)-maxDeprecationWarnings
		}
		for _, d := range shown {
			b.WriteString("\n")
			b.WriteString(d)
		}
		if extra > 0 {
			fmt.Fprintf(&b, "\n…+%d more deprecation warnings", extra)
		}
	}

	if fundingCount+noiseCount > 0 {
		fmt.Fprintf(&b, "\n…+%d noise lines", fundingCount+noiseCount)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}
