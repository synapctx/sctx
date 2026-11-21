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

// maxAuditPlainLines bounds the non-error lines kept when an audit produced
// no vulnerability summary (a failed run); error lines are never counted
// against it.
const maxAuditPlainLines = 20

// vulnerabilitySummaryRe matches npm/pnpm/yarn audit's terse result line,
// e.g. "5 vulnerabilities (2 moderate, 3 high)", "2 critical severity
// vulnerabilities" or "found 0 vulnerabilities". It is anchored on the count
// because advisory titles ("... Prototype Pollution Vulnerability in ...")
// also contain the word.
var vulnerabilitySummaryRe = regexp.MustCompile(`(?i)^(found\s+)?\d+\s.*\bvulnerabilit(y|ies)\b`)

// aggressiveAudit collapses `audit` output to the vulnerability summary
// line plus a capped list of per-advisory paragraphs. Without a summary (a
// failed audit: network error, registry refusal) every error line is kept
// and whatever else is dropped is counted in a marker — a non-zero exit never
// loses lines silently.
func aggressiveAudit(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	if len(rawOut) == 0 && len(rawErr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var summaryLine string
	var outErrors []string
	var allLines []string // every non-blank line, stdout then stderr
	hasError := false
	scan := func(raw []byte, isStdout bool) {
		for _, l := range splitLines(raw) {
			t := strings.TrimSpace(l)
			if t == "" {
				continue
			}
			allLines = append(allLines, t)
			if isErrorLine(t) {
				hasError = true
				if isStdout {
					outErrors = append(outErrors, t)
				}
				continue
			}
			if summaryLine == "" && vulnerabilitySummaryRe.MatchString(t) {
				summaryLine = t
			}
		}
	}
	scan(rawOut, true)
	stdoutLines := len(allLines)
	scan(rawErr, false)

	if summaryLine == "" {
		if !hasError && in.ExitCode == 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return keepAuditLines(allLines, stdoutLines, len(rawErr) > 0), nil
	}

	blocks := paragraphBlocks(rawOut)
	var advisories []string
	hints := 0
	for _, blk := range blocks {
		trimmed := strings.TrimSpace(blk)
		switch {
		case strings.Contains(blk, summaryLine), strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "To address"):
			hints++
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
	for _, l := range outErrors {
		b.WriteByte('\n')
		b.WriteString(l)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "\n…+%d more advisories", extra)
	}
	if hints > 0 {
		fmt.Fprintf(&b, "\n…+%d fix-hint blocks", hints)
	}

	return format.Rendered{Body: []byte(b.String()), Elided: extra > 0 || hints > 0}, nil
}

// keepAuditLines renders a summary-less audit: every error line survives, other
// STDOUT lines up to maxAuditPlainLines, and the rest is counted. Every stderr
// line (lines[stdoutLines:]) is kept: stderr is folded into the body, so a
// stderr line dropped here would be lost entirely, and it is where npm puts the
// reason a failed audit failed. Stderr is folded only when it was present.
func keepAuditLines(lines []string, stdoutLines int, foldStderr bool) format.Rendered {
	var kept []string
	plain, dropped := 0, 0
	for i, t := range lines {
		if i >= stdoutLines || isErrorLine(t) {
			kept = append(kept, t)
			continue
		}
		if plain < maxAuditPlainLines {
			plain++
			kept = append(kept, t)
			continue
		}
		dropped++
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("…+%d lines", dropped))
	}
	return format.Rendered{
		Body:       []byte(strings.Join(kept, "\n")),
		FoldStderr: foldStderr,
		Elided:     dropped > 0,
	}
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
