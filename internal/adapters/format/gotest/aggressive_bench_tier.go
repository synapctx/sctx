package gotest

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// isBenchOutput reports whether lines look like `go test -bench` output
// (at least one "Benchmark..." result line).
func isBenchOutput(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, "Benchmark") {
			return true
		}
	}
	return false
}

// renderBenchTest keeps benchmark result lines (name, iterations, ns/op,
// B/op, allocs/op), any coverage line, and the final PASS/FAIL/ok line;
// everything else (build progress, blank lines, === RUN noise) is dropped.
func renderBenchTest(lines []string, stderr []byte) format.Rendered {
	var kept []string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "Benchmark"):
			kept = append(kept, l)
		case l == "PASS", strings.HasPrefix(l, "FAIL"):
			kept = append(kept, l)
		case strings.HasPrefix(l, "ok\t") || strings.HasPrefix(l, "ok  "):
			kept = append(kept, l)
		case strings.Contains(l, "coverage:"):
			kept = append(kept, l)
		}
	}

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
		body = "(no benchmark result lines captured)\n"
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}
}
