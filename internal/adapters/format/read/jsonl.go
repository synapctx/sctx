package read

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// minJSONLLines is the minimum number of independently-JSON-valid lines
// required before stdout is treated as JSONL/NDJSON (e.g. a *.jsonl log)
// rather than a coincidental handful of JSON-looking lines. Below this the
// tier stays inapplicable rather than guess.
const minJSONLLines = 8

// keepJSONLLines is how many leading lines are kept — whitespace-compacted
// only, never content-truncated — before the "…+N more" marker replaces
// the remainder.
const keepJSONLLines = 5

// renderJSONL detects newline-delimited JSON: every non-blank line of raw
// parses as an independent JSON value. If so and the line count clears
// minJSONLLines, it keeps the first keepJSONLLines lines (compacted with
// json.Compact, which only removes insignificant whitespace — no field is
// dropped, truncated, or altered) and replaces the remaining lines with an
// explicit "…+N more JSON lines" marker.
//
// ok is false whenever any non-blank line fails to parse as JSON or the
// line count is too small to be confidently NDJSON; the caller must then
// fall back to a lower tier rather than risk hiding non-JSON content (e.g.
// a source file that happens to contain a couple of `{`/`}`-only lines).
func renderJSONL(raw []byte) (format.Rendered, bool) {
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, false
	}

	jsonLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !json.Valid([]byte(trimmed)) {
			return format.Rendered{}, false
		}
		jsonLines = append(jsonLines, trimmed)
	}
	if len(jsonLines) < minJSONLLines {
		return format.Rendered{}, false
	}

	keep := keepJSONLLines
	if keep > len(jsonLines) {
		keep = len(jsonLines)
	}

	var buf bytes.Buffer
	for i := 0; i < keep; i++ {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(jsonLines[i])); err != nil {
			// Shouldn't happen given json.Valid passed above; refuse to
			// guess rather than risk emitting something wrong.
			return format.Rendered{}, false
		}
		buf.Write(compact.Bytes())
		buf.WriteByte('\n')
	}

	rest := len(jsonLines) - keep
	if rest > 0 {
		fmt.Fprintf(&buf, "…+%d more JSON lines\n", rest)
	}

	body := bytes.TrimRight(buf.Bytes(), "\n")
	if len(body) == 0 || len(body) >= len(raw) {
		return format.Rendered{}, false
	}

	return format.Rendered{
		Body: body,
		Note: fmt.Sprintf("jsonl: kept %d of %d lines", keep, len(jsonLines)),
	}, true
}
