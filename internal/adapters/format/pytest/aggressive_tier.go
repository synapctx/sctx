package pytest

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// bannerRE matches pytest's "=== text ===" section/summary banner lines.
var bannerRE = regexp.MustCompile(`^={3,}.+={3,}$`)

// underscoreRE matches pytest's "___ test name ___" per-failure headers
// inside the FAILURES/ERRORS sections.
var underscoreRE = regexp.MustCompile(`^_{3,}.+_{3,}$`)

// durationRE matches the "in 1.23s" fragment carried by the final summary
// banner, distinguishing it from section banners like "=== FAILURES ===".
var durationRE = regexp.MustCompile(`in \d+(\.\d+)?s`)

// aggressiveRender implements the aggressive tier for `pytest`'s default
// (or -q) text output. All-pass runs collapse to a single summary line;
// failing/erroring runs keep the final summary, the short FAILED/ERROR
// node-id list, and a short excerpt per failure with long tracebacks
// collapsed behind a "…+N lines" marker.
func aggressiveRender(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("pytest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("pytest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := splitLines(string(stdout))
	if !looksLikePytest(lines, string(stderr)) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	summary, summaryIdx := finalSummary(lines)

	if summary != "" && isCleanSummary(summary) {
		return renderClean(summary, stderr), nil
	}

	return renderWithFailures(lines, summary, summaryIdx, stderr), nil
}

// looksLikePytest reports whether stdout carries at least one recognizable
// pytest banner line ("=== ... ===" section/summary marker). Without one,
// the aggressive tier does not apply and the chain should degrade.
func looksLikePytest(lines []string, stderr string) bool {
	for _, l := range lines {
		if bannerRE.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return bannerRE.MatchString(strings.TrimSpace(stderr))
}

// finalSummary returns the last banner line carrying a "in X.Ys" duration
// (pytest's final result line, e.g. "==== 5 failed, 2 passed in 1.23s
// ====") and its index in lines, or ("", -1) if none is present.
func finalSummary(lines []string) (string, int) {
	for i, line := range slices.Backward(lines) {
		l := strings.TrimSpace(line)
		if bannerRE.MatchString(l) && durationRE.MatchString(l) {
			return l, i
		}
	}
	return "", -1
}

// isCleanSummary reports whether a final summary line indicates no
// failures and no collection/errors worth surfacing (only "passed",
// "skipped", "deselected", "xfailed", "xpassed", or "no tests ran").
func isCleanSummary(summary string) bool {
	lower := strings.ToLower(summary)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "error") {
		return false
	}
	return true
}

// renderClean collapses an all-clean pytest run to its final summary line,
// still surfacing any stderr content so error signal is never dropped.
func renderClean(summary string, stderr []byte) format.Rendered {
	var b strings.Builder
	b.WriteString(bannerText(summary))
	b.WriteByte('\n')

	foldStderr := false
	if len(stderr) > 0 {
		b.Write(stderr)
		if !strings.HasSuffix(string(stderr), "\n") {
			b.WriteByte('\n')
		}
		foldStderr = true
	}

	return format.Rendered{Body: []byte(b.String()), FoldStderr: foldStderr}
}

// section identifies which part of pytest's failing-run output a line
// belongs to.
type section int

const (
	sectionPreamble section = iota
	sectionFailures
	sectionErrors
	sectionSummaryInfo
	sectionOther
)

// renderWithFailures keeps the FAILURES/ERRORS excerpts, the short test
// summary info block (FAILED/ERROR node ids), and the final summary line.
// Preamble noise (platform/rootdir/plugins, progress dots), warnings, and
// full tracebacks (beyond a short excerpt) are dropped with explicit
// elision markers.
func renderWithFailures(lines []string, summary string, summaryIdx int, stderr []byte) format.Rendered {
	var kept []string
	cur := sectionPreamble
	droppedInBlock := 0
	inBlock := false

	flushDrop := func() {
		if droppedInBlock > 0 {
			kept = append(kept, fmt.Sprintf("        …+%d lines", droppedInBlock))
			droppedInBlock = 0
		}
	}

	for i, raw := range lines {
		if i == summaryIdx {
			continue // emitted separately, at the end.
		}
		l := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(l)

		if bannerRE.MatchString(trimmed) {
			flushDrop()
			inBlock = false
			switch {
			case strings.Contains(trimmed, "session starts"):
				cur = sectionPreamble
			case strings.EqualFold(bannerText(trimmed), "FAILURES"):
				cur = sectionFailures
			case strings.EqualFold(bannerText(trimmed), "ERRORS"):
				cur = sectionErrors
			case strings.Contains(strings.ToLower(trimmed), "short test summary info"):
				cur = sectionSummaryInfo
			default:
				cur = sectionOther // e.g. "warnings summary", "slowest durations"
			}
			continue
		}

		switch cur {
		case sectionPreamble, sectionOther:
			// platform/rootdir/plugins, progress dots, warnings summary,
			// durations report: no error signal, drop silently.
		case sectionFailures, sectionErrors:
			if underscoreRE.MatchString(trimmed) {
				flushDrop()
				inBlock = true
				kept = append(kept, l)
				continue
			}
			if !inBlock {
				continue
			}
			if strings.HasPrefix(trimmed, "E ") || strings.HasPrefix(trimmed, "E\t") ||
				isFileLineRef(trimmed) {
				kept = append(kept, l)
				continue
			}
			if cur == sectionErrors && trimmed != "" && !strings.HasPrefix(trimmed, ">") {
				// Collection errors are usually short; keep the plain
				// message lines (e.g. "ImportError: ...") verbatim.
				kept = append(kept, l)
				continue
			}
			if trimmed == "" {
				continue
			}
			droppedInBlock++
		case sectionSummaryInfo:
			if trimmed != "" {
				kept = append(kept, l)
			}
		}
	}
	flushDrop()

	body := strings.Join(kept, "\n")
	if body != "" {
		body += "\n"
	}
	if summary != "" {
		body += bannerText(summary) + "\n"
	}

	foldStderr := false
	if len(stderr) > 0 {
		body += string(stderr)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		foldStderr = true
	}
	if body == "" {
		body = "pytest: failed (no diagnostic output captured)\n"
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}
}

// fileLineRefRE matches pytest's trailing "path/to/file.py:10: AssertionError"
// reference line that closes out a traceback.
var fileLineRefRE = regexp.MustCompile(`^\S+\.py:\d+: \S+`)

// isFileLineRef reports whether a trimmed line is pytest's closing
// "file.py:LINE: ExceptionType" reference.
func isFileLineRef(trimmed string) bool {
	return fileLineRefRE.MatchString(trimmed)
}

// bannerText strips the "=== " / " ===" padding from a pytest banner line,
// returning just its inner text.
func bannerText(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "="))
}
