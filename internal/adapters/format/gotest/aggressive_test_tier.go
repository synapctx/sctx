package gotest

import (
	"fmt"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveTest implements the aggressive tier for `go test`. It handles
// -json (test2json) output structurally, always preserves data-race reports
// verbatim, keeps benchmark result lines for -bench runs, and otherwise
// applies the standard pass/fail text summary (which also now retains any
// -cover coverage line). Passing runs collapse to a one-line summary;
// failing runs keep every failure block and drop passing-package noise.
func aggressiveTest(in format.Input) (format.Rendered, error) {
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

	if isJSONTestOutput(stdout) {
		if rendered, ok := renderJSONTest(stdout, stderr); ok {
			return rendered, nil
		}
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := splitLines(string(stdout))

	// Data races are critical error signal: never elide them, regardless
	// of exit code or benchmark mode.
	if hasDataRace(lines) {
		return renderRaceTest(lines, stderr), nil
	}

	if isBenchOutput(lines) {
		return renderBenchTest(lines, stderr), nil
	}

	if in.ExitCode == 0 {
		// -v output survives its own tier: the passing renderer counts "ok"
		// lines and drops the rest, which under -v is the per-test detail and
		// the t.Log output the flag was typed to produce.
		if verboseMode(in) {
			return renderPassingVerboseTest(lines, stderr, in.Duration), nil
		}
		return renderPassingTest(lines, stderr, in.Duration), nil
	}
	return renderFailingTest(lines, stderr), nil
}

// renderPassingTest collapses an all-green `go test` run to a single
// summary line, still surfacing any stderr content (e.g. a stray build
// warning) so error signal is never dropped.
func renderPassingTest(lines []string, stderr []byte, dur time.Duration) format.Rendered {
	var okCount, notfCount, cachedCount int
	var coverage []string
	for _, l := range lines {
		if cov, ok := coverageSuffix(l); ok {
			coverage = append(coverage, cov)
		}
		switch {
		case strings.HasPrefix(l, "ok\t") || strings.HasPrefix(l, "ok  "):
			okCount++
			if strings.Contains(l, "(cached)") {
				cachedCount++
			}
		case strings.Contains(l, "[no test files]"):
			notfCount++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ok: %d packages", okCount)
	if dur > 0 {
		fmt.Fprintf(&b, ", %s", dur.Round(time.Millisecond))
	}
	if notfCount > 0 {
		fmt.Fprintf(&b, "; %d no test files", notfCount)
	}
	if cachedCount > 0 {
		fmt.Fprintf(&b, "; %d cached", cachedCount)
	}
	b.WriteByte('\n')
	for _, cov := range dedupeLines(coverage) {
		b.WriteString(cov)
		b.WriteByte('\n')
	}

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

// renderFailingTest keeps every --- FAIL block and FAIL <pkg> line in
// full, drops "ok <pkg>" noise (summarized as a count), and folds in any
// stderr build errors verbatim.
func renderFailingTest(lines []string, stderr []byte) format.Rendered {
	var kept []string
	var coverage []string
	var okOthers, notfCount int
	inFailBlock := false

	for _, l := range lines {
		// Standalone "coverage: NN.N% of statements" lines (from -v mode)
		// fall through to the default case below and are kept verbatim
		// already; only capture the suffix here for "ok"-prefixed lines,
		// whose coverage info would otherwise be lost when collapsed to
		// okOthers.
		if strings.HasPrefix(l, "ok\t") || strings.HasPrefix(l, "ok  ") {
			if cov, ok := coverageSuffix(l); ok {
				coverage = append(coverage, cov)
			}
		}
		switch {
		case strings.HasPrefix(l, "--- FAIL"):
			inFailBlock = true
			kept = append(kept, l)
		case strings.HasPrefix(l, "--- PASS"):
			inFailBlock = false
		case strings.HasPrefix(l, "=== RUN"), strings.HasPrefix(l, "=== PAUSE"), strings.HasPrefix(l, "=== CONT"):
			// verbose progress noise, not error signal.
		case strings.HasPrefix(l, "FAIL\t") || strings.HasPrefix(l, "FAIL "):
			inFailBlock = false
			kept = append(kept, l)
		case l == "FAIL":
			inFailBlock = false
			kept = append(kept, l)
		case strings.HasPrefix(l, "ok\t") || strings.HasPrefix(l, "ok  "):
			inFailBlock = false
			okOthers++
		case strings.Contains(l, "[no test files]"):
			inFailBlock = false
			notfCount++
		default:
			if inFailBlock || strings.TrimSpace(l) != "" {
				// Inside a failure block, or an otherwise ambiguous
				// non-blank line: never drop possible error signal.
				kept = append(kept, l)
			}
		}
	}

	if okOthers > 0 {
		kept = append(kept, fmt.Sprintf("ok: %d other packages", okOthers))
	}
	if notfCount > 0 {
		kept = append(kept, fmt.Sprintf("no test files: %d packages", notfCount))
	}
	kept = append(kept, dedupeLines(coverage)...)

	body := strings.Join(kept, "\n")
	if body != "" {
		body += "\n"
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
		body = "FAIL (no diagnostic output captured)\n"
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}
}
