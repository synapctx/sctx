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

// plainSummaryRE matches the banner-less verdict line `pytest -q` prints:
// "3 failed, 3 passed, 1 skipped in 0.02s" (optionally with "(0:00:01)").
var plainSummaryRE = regexp.MustCompile(`^(\d+ [a-z]+( \(\d+\))?(, \d+ [a-z]+( \(\d+\))?)*|no tests ran) in \d+(\.\d+)?s( \(\d+:\d{2}:\d{2}\))?$`)

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

	if summary == "" {
		// No final result line: an interrupted or foreign run. Rendering
		// failure blocks without a verdict could hide why it stopped.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return renderWithFailures(lines, summary, summaryIdx, stderr), nil
}

// looksLikePytest reports whether stdout carries at least one recognizable
// pytest banner line ("=== ... ===" section/summary marker). Without one,
// the aggressive tier does not apply and the chain should degrade.
func looksLikePytest(lines []string, stderr string) bool {
	for _, l := range lines {
		if isPytestBanner(strings.TrimSpace(l)) {
			return true
		}
	}
	for _, l := range slices.Backward(lines) {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if plainSummaryRE.MatchString(strings.TrimSpace(l)) {
			return true
		}
		break
	}
	return isPytestBanner(strings.TrimSpace(stderr))
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
	// -q drops the "=== ... ===" padding: the verdict is the bare last line,
	// e.g. "3 failed, 3 passed in 0.02s".
	for i, line := range slices.Backward(lines) {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if plainSummaryRE.MatchString(l) {
			return l, i
		}
		break
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
	skipFrame := false
	passedRows := 0

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
				kept = append(kept, shortHeader(trimmed))
				skipFrame = false
				continue
			}
			if !inBlock {
				continue
			}
			// The interpreter's own import machinery frame ("importlib/__init__.py:88:
			// in import_module" and its source/caret lines) is the same boilerplate
			// in every collection error; the user's frames follow it.
			if importlibFrameRE.MatchString(trimmed) {
				skipFrame = true
				droppedInBlock++
				continue
			}
			if skipFrame {
				if strings.HasPrefix(l, "    ") {
					droppedInBlock++
					continue
				}
				skipFrame = false
			}
			// The failing source line ("> assert add(1, 2) == 4") names the
			// statement that failed; the E lines only give its values.
			if strings.HasPrefix(trimmed, ">") {
				kept = append(kept, l)
				continue
			}
			if strings.HasPrefix(trimmed, "E ") || strings.HasPrefix(trimmed, "E\t") ||
				isFileLineRef(trimmed) {
				kept = append(kept, l)
				continue
			}
			if cur == sectionErrors && trimmed != "" {
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
			if strings.HasPrefix(trimmed, "PASSED ") {
				// -rA lists every passing test; the final line already counts them.
				passedRows++
				continue
			}
			if passedRows > 0 {
				kept = append(kept, fmt.Sprintf("PASSED ×%d", passedRows))
				passedRows = 0
			}
			if trimmed != "" {
				kept = append(kept, l)
			}
		}
	}
	flushDrop()
	if passedRows > 0 {
		kept = append(kept, fmt.Sprintf("PASSED ×%d", passedRows))
	}

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

// isPytestBanner reports a "=== text ===" banner that carries words; a bare
// rule of '=' signs (unittest's separator, a Markdown heading) is not pytest.
func isPytestBanner(l string) bool {
	return bannerRE.MatchString(l) && strings.ContainsAny(l, "abcdefghijklmnopqrstuvwxyz")
}

// importlibFrameRE matches the "…/importlib/__init__.py:88: in import_module"
// frame pytest prints at the top of every ImportError during collection.
var importlibFrameRE = regexp.MustCompile(`/importlib/__init__\.py:\d+: in import_module$`)

// shortHeader reduces "____ test_x ____" (padded to 80 columns) to
// "___ test_x ___".
func shortHeader(trimmed string) string {
	name := strings.TrimSpace(strings.Trim(trimmed, "_"))
	if name == "" {
		return trimmed
	}
	return "___ " + name + " ___"
}
