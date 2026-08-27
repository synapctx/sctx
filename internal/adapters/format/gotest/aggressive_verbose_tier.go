package gotest

import (
	"fmt"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
)

// verboseFlags are the spellings that turn on per-test output. `-vet=off` is
// deliberately NOT one of them: matching on a "-v" PREFIX would swallow it, so
// these are compared exactly after any "=value" is trimmed.
var verboseFlags = map[string]bool{
	"-v": true, "--v": true, // go test -v, -v=true
	"-test.v": true, // the compiled test binary's own spelling
}

// verboseMode reports whether this invocation asked for per-test output.
func verboseMode(in format.Input) bool {
	for _, a := range in.Argv {
		if i := strings.IndexByte(a, '='); i > 0 {
			a = a[:i]
		}
		if verboseFlags[a] {
			return true
		}
	}
	return false
}

// renderPassingVerboseTest renders a PASSING `go test -v` run.
//
// WHY THIS TIER EXISTS. renderPassingTest collapses a green run to "ok: N
// packages" by counting "ok" lines and discarding everything else. Under -v
// "everything else" is the entire reason the flag was typed: t.Log/t.Logf output
// exists ONLY in verbose mode, so summarizing it away deletes output that cannot
// be recovered from anywhere — the same failure the inspection-mode flags guard
// against, reached through a flag nobody thought to list.
//
// Measured before the fix: `go test -v` over a package whose only test logs
// "before=0 after=1" rendered as "ok: 1 packages, 493ms". The log line, which was
// the whole point of the run, was gone.
//
// So this keeps every per-test RESULT line and every log line, and drops only
// "=== RUN"/"=== PAUSE"/"=== CONT", which are progress markers that carry no
// information a result line does not already give. That is a real reduction —
// those are roughly a third of a verbose run, more with subtests — without
// touching anything the caller asked to see.
func renderPassingVerboseTest(lines []string, stderr []byte, dur time.Duration) format.Rendered {
	var kept []string
	var okCount, notfCount, cachedCount, dropped int

	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "=== RUN"), strings.HasPrefix(l, "=== PAUSE"), strings.HasPrefix(l, "=== CONT"):
			dropped++
		case strings.HasPrefix(l, "ok\t") || strings.HasPrefix(l, "ok  "):
			okCount++
			if strings.Contains(l, "(cached)") {
				cachedCount++
			}
		case strings.Contains(l, "[no test files]"):
			notfCount++
		case l == "PASS":
			// Per-package terminator, redundant with the "ok" line that follows.
			dropped++
		default:
			kept = append(kept, l)
		}
	}

	var b strings.Builder
	for _, l := range kept {
		b.WriteString(l)
		b.WriteByte('\n')
	}

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
	// The marker is the contract: an elision is always counted and never silent.
	if dropped > 0 {
		fmt.Fprintf(&b, "; progress lines ×%d", dropped)
	}
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
