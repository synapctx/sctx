package gotest

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// hasDataRace reports whether lines contain a race-detector report
// ("WARNING: DATA RACE").
func hasDataRace(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "WARNING: DATA RACE") {
			return true
		}
	}
	return false
}

// isEqualsDelimiter reports whether l is one of the race detector's
// "==================" block delimiter lines.
func isEqualsDelimiter(l string) bool {
	trimmed := strings.TrimSpace(l)
	return len(trimmed) > 0 && strings.Trim(trimmed, "=") == ""
}

// renderRaceTest keeps every data-race report block verbatim (delimiters,
// goroutine stacks, and all) plus the surrounding FAIL signal, since a race
// report is critical error signal that must never be elided. Verbose
// "=== RUN"/"=== PAUSE"/"=== CONT" progress noise outside race blocks is
// dropped, which is where the compression comes from.
func renderRaceTest(lines []string, stderr []byte) format.Rendered {
	var kept []string
	inRace := false

	for i, l := range lines {
		switch {
		case strings.Contains(l, "WARNING: DATA RACE"):
			inRace = true
			if i > 0 && isEqualsDelimiter(lines[i-1]) && (len(kept) == 0 || kept[len(kept)-1] != lines[i-1]) {
				kept = append(kept, lines[i-1])
			}
			kept = append(kept, l)
		case inRace:
			kept = append(kept, l)
			if isEqualsDelimiter(l) {
				inRace = false
			}
		case strings.HasPrefix(l, "=== RUN"), strings.HasPrefix(l, "=== PAUSE"), strings.HasPrefix(l, "=== CONT"):
			// verbose progress noise, not error signal.
		case strings.Contains(l, "data race(s) detected"), strings.Contains(l, "Found") && strings.Contains(l, "data race"):
			kept = append(kept, l)
		case strings.HasPrefix(l, "--- FAIL"), strings.HasPrefix(l, "FAIL"):
			kept = append(kept, l)
		default:
			if strings.TrimSpace(l) != "" {
				kept = append(kept, l)
			}
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
		body = "DATA RACE detected (no report body captured)\n"
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}
}
