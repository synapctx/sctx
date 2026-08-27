package gotest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxModGraphEdges caps how many `go mod graph` dependency-edge lines are
// kept verbatim before collapsing the remainder to a count marker.
const maxModGraphEdges = 30

// modNoisePrefixes are `go mod` progress lines that carry no diagnostic
// value once collapsed to a count; every other line (including error and
// warning diagnostics such as "go: updates to go.mod needed" or "missing
// go.sum entry") is preserved verbatim.
var modNoisePrefixes = []string{
	"go: downloading",
	"go: finding",
	"go: extracting",
}

// aggressiveMod implements the aggressive tier for `go mod` (tidy,
// download, verify, why, graph, ...). Fetch progress noise collapses to a
// single "…+N modules" marker; `go mod graph` edge lists are capped.
// Clean, quiet success (the common case for tidy/download) has nothing to
// compress, so it degrades to the next tier.
func aggressiveMod(in format.Input) (format.Rendered, error) {
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

	kept, noiseCount := filterModNoise(splitLines(string(stderr)))
	outLines, graphNoise := filterModNoise(splitLines(string(stdout)))
	noiseCount += graphNoise

	if isModGraph(in) {
		outLines = capModGraphEdges(outLines)
	}

	all := append(kept, outLines...)
	if noiseCount > 0 {
		all = append([]string{fmt.Sprintf("…+%d modules", noiseCount)}, all...)
	}

	body := strings.Join(all, "\n")
	if body != "" {
		body += "\n"
	}
	if body == "" {
		// Nothing but noise and nothing failed: quiet success, no value in
		// rendering a synthetic line over letting a later tier (or
		// verbatim) handle the (likely tiny) remainder.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(body), FoldStderr: len(stderr) > 0}, nil
}

// filterModNoise drops `go mod` fetch-progress lines (downloading/finding/
// extracting), returning the surviving lines and a count of dropped ones.
// Every other line, including diagnostics, is kept verbatim.
func filterModNoise(lines []string) ([]string, int) {
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, l := range lines {
		if isModNoise(l) {
			dropped++
			continue
		}
		kept = append(kept, l)
	}
	return kept, dropped
}

// isModNoise reports whether l is a `go mod` fetch-progress line.
func isModNoise(l string) bool {
	for _, prefix := range modNoisePrefixes {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// isModGraph reports whether the invocation is `go mod graph`, whose stdout
// is a flat "module dependency" edge list that grows with module count.
func isModGraph(in format.Input) bool {
	if slices.Contains(in.Argv, "graph") {
		return true
	}
	return strings.Contains(in.Command, "mod graph")
}

// capModGraphEdges keeps the first maxModGraphEdges edge lines from `go mod
// graph` output, appending an explicit "…+N more edges" marker.
func capModGraphEdges(lines []string) []string {
	if len(lines) <= maxModGraphEdges {
		return lines
	}
	more := len(lines) - maxModGraphEdges
	out := make([]string, 0, maxModGraphEdges+1)
	out = append(out, lines[:maxModGraphEdges]...)
	out = append(out, fmt.Sprintf("…+%d more edges", more))
	return out
}
