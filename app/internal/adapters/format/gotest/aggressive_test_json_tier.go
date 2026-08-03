package gotest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxJSONFailedTests caps how many failed-test names `go test -json`
// summaries list before collapsing the remainder to a count marker.
const maxJSONFailedTests = 20

// maxJSONFailOutputLines caps how many captured Output lines are kept per
// failed test, to avoid re-inflating the summary with verbose logs.
const maxJSONFailOutputLines = 5

// testJSONEvent mirrors the subset of cmd/test2json's TestEvent fields this
// tier needs.
type testJSONEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// isJSONTestOutput reports whether raw looks like `go test -json` output:
// every non-blank line is a standalone valid JSON value.
func isJSONTestOutput(raw []byte) bool {
	lines := splitLines(string(raw))
	seen := 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !json.Valid([]byte(l)) {
			return false
		}
		seen++
	}
	return seen > 0
}

// renderJSONTest parses test2json event lines into a pass/fail summary with
// failed-test names and a capped excerpt of their output. It reports ok=
// false when the structure is unexpected enough that a byte-accurate parse
// isn't worth the risk, so the caller can degrade to the next tier.
func renderJSONTest(raw []byte, stderr []byte) (format.Rendered, bool) {
	lines := splitLines(string(raw))
	if len(lines) == 0 {
		return format.Rendered{}, false
	}

	var passCount, failCount, skipCount int
	var failedTests []string
	failOutput := map[string][]string{}

	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var ev testJSONEvent
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			return format.Rendered{}, false
		}
		if ev.Test == "" {
			continue // package-level event; the pass/fail counts below cover tests.
		}
		key := ev.Package + "." + ev.Test
		switch ev.Action {
		case "pass":
			passCount++
		case "fail":
			failCount++
			failedTests = append(failedTests, key)
		case "skip":
			skipCount++
		case "output":
			if len(failOutput[key]) < maxJSONFailOutputLines {
				failOutput[key] = append(failOutput[key], strings.TrimRight(ev.Output, "\n"))
			}
		}
	}

	if passCount == 0 && failCount == 0 && skipCount == 0 {
		return format.Rendered{}, false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "go test -json: %d passed, %d failed", passCount, failCount)
	if skipCount > 0 {
		fmt.Fprintf(&b, ", %d skipped", skipCount)
	}
	b.WriteByte('\n')

	capped := failedTests
	if len(failedTests) > maxJSONFailedTests {
		more := len(failedTests) - maxJSONFailedTests
		capped = append(append([]string{}, failedTests[:maxJSONFailedTests]...), fmt.Sprintf("…+%d more failed tests", more))
	}
	for _, name := range capped {
		b.WriteString("FAIL ")
		b.WriteString(name)
		b.WriteByte('\n')
		for _, out := range failOutput[name] {
			if strings.TrimSpace(out) == "" {
				continue
			}
			b.WriteString("    ")
			b.WriteString(out)
			b.WriteByte('\n')
		}
	}

	foldStderr := false
	body := b.String()
	if len(stderr) > 0 {
		body += string(stderr)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		foldStderr = true
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}, true
}
