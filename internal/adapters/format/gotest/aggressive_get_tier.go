package gotest

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxGetChanges caps how many "go: upgraded/downgraded/added" version-change
// lines are kept verbatim before collapsing the remainder to a count marker.
const maxGetChanges = 20

// getNoisePrefixes are `go get` fetch-progress lines with no lasting value
// once collapsed to a count.
var getNoisePrefixes = []string{
	"go: downloading",
	"go: finding",
	"go: extracting",
}

// getChangePrefixes are `go get` lines describing an actual module version
// change; these are the useful signal a caller wants to see (capped, not
// dropped).
var getChangePrefixes = []string{
	"go: added",
	"go: upgraded",
	"go: downgraded",
	"go: removed",
}

// aggressiveGet implements the aggressive tier for `go get`. Fetch-progress
// noise collapses to a count marker; version-change lines are capped and
// kept; every other line (errors, ambiguous import diagnostics, etc.) is
// preserved verbatim.
func aggressiveGet(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	allLines := append(splitLines(string(stdout)), splitLines(string(stderr))...)

	var changes, other []string
	noiseCount := 0
	for _, l := range allLines {
		switch {
		case hasAnyPrefix(l, getNoisePrefixes):
			noiseCount++
		case hasAnyPrefix(l, getChangePrefixes):
			changes = append(changes, l)
		default:
			if strings.TrimSpace(l) != "" {
				other = append(other, l)
			}
		}
	}

	changes = capLines(changes, maxGetChanges, "more version changes")

	var out []string
	if noiseCount > 0 {
		out = append(out, fmt.Sprintf("…+%d modules fetched", noiseCount))
	}
	out = append(out, changes...)
	out = append(out, other...)

	body := strings.Join(out, "\n")
	if body != "" {
		body += "\n"
	}
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(body), FoldStderr: len(stderr) > 0}, nil
}

// hasAnyPrefix reports whether l starts with any of prefixes.
func hasAnyPrefix(l string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// capLines keeps the first max entries of lines, appending an explicit
// "…+N <label>" marker for the remainder.
func capLines(lines []string, max int, label string) []string {
	if len(lines) <= max {
		return lines
	}
	more := len(lines) - max
	out := make([]string, 0, max+1)
	out = append(out, lines[:max]...)
	out = append(out, fmt.Sprintf("…+%d %s", more, label))
	return out
}
