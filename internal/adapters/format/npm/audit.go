package npm

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxAdvisoryBlocks caps how many per-package advisory paragraphs are kept
// verbatim from an audit report before eliding the rest.
const maxAdvisoryBlocks = 3

// vulnerabilitySummaryRe matches npm/pnpm/yarn audit's terse result line,
// e.g. "5 vulnerabilities (2 moderate, 3 high)" or "found 0 vulnerabilities".
var vulnerabilitySummaryRe = regexp.MustCompile(`(?i)\bvulnerabilit(y|ies)\b`)

// aggressiveAudit collapses `audit` output to the vulnerability summary
// line plus a capped list of per-advisory paragraphs; a genuine failure
// (network error, no summary produced) keeps the error block verbatim.
func aggressiveAudit(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	if len(rawOut) == 0 && len(rawErr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var errorLines []string
	var summaryLine string
	for _, l := range append(splitLines(rawOut), splitLines(rawErr)...) {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if isErrorLine(t) {
			errorLines = append(errorLines, t)
			continue
		}
		if summaryLine == "" && vulnerabilitySummaryRe.MatchString(t) {
			summaryLine = t
		}
	}

	if summaryLine == "" {
		if len(errorLines) == 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return format.Rendered{
			Body:       []byte(strings.Join(errorLines, "\n")),
			FoldStderr: len(rawErr) > 0,
		}, nil
	}

	blocks := paragraphBlocks(rawOut)
	var advisories []string
	for _, blk := range blocks {
		if strings.Contains(blk, summaryLine) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(blk), "#") {
			continue
		}
		advisories = append(advisories, blk)
	}

	shown, extra := advisories, 0
	if len(advisories) > maxAdvisoryBlocks {
		shown, extra = advisories[:maxAdvisoryBlocks], len(advisories)-maxAdvisoryBlocks
	}

	var b strings.Builder
	b.WriteString(summaryLine)
	for _, blk := range shown {
		b.WriteString("\n\n")
		b.WriteString(blk)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "\n…+%d more advisories", extra)
	}

	return format.Rendered{Body: []byte(b.String())}, nil
}

// paragraphBlocks splits raw output into blocks of consecutive non-blank
// lines, separated by one or more blank lines.
func paragraphBlocks(raw []byte) []string {
	lines := splitLines(raw)
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		cur = append(cur, l)
	}
	flush()
	return blocks
}
