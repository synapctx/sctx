// Package jsonlines detects and safely bounds JSON Lines / NDJSON streams.
package jsonlines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

const (
	MinRecords  = 8
	KeepRecords = 5
)

type Classification uint8

const (
	NotJSONLines Classification = iota
	ValidJSONLines
	MixedJSONLines
)

// Classify requires a useful stream-sized sample. A stream with at least
// MinRecords nonblank records and a mixture of valid and invalid JSON is marked
// mixed so callers can preserve it verbatim instead of treating it as text.
func Classify(raw []byte) Classification {
	lines := collapse.SplitLines(raw)
	valid, invalid := 0, 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// JSON permits scalar values, but treating arbitrary lines such as
		// `1`, `2`, `3` as a record stream would make generic coverage eat
		// ordinary command output. NDJSON emitted by developer tools uses
		// structured object/array records, so require that safer signature.
		if isStructuredRecord(trimmed) && json.Valid([]byte(trimmed)) {
			valid++
		} else {
			invalid++
		}
	}
	if valid+invalid < MinRecords || valid == 0 {
		return NotJSONLines
	}
	if invalid > 0 {
		return MixedJSONLines
	}
	return ValidJSONLines
}

func isStructuredRecord(record string) bool {
	return strings.HasPrefix(record, "{") || strings.HasPrefix(record, "[")
}

// Render compacts the first KeepRecords records without altering their JSON
// values and replaces the remainder with one exact record-count marker.
func Render(raw []byte) (format.Rendered, bool) {
	if Classify(raw) != ValidJSONLines {
		return format.Rendered{}, false
	}
	lines := collapse.SplitLines(raw)
	jsonRecords := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			jsonRecords = append(jsonRecords, trimmed)
		}
	}

	keep := KeepRecords
	if keep > len(jsonRecords) {
		keep = len(jsonRecords)
	}
	var buf bytes.Buffer
	for i := 0; i < keep; i++ {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(jsonRecords[i])); err != nil {
			return format.Rendered{}, false
		}
		buf.Write(compact.Bytes())
		buf.WriteByte('\n')
	}
	if rest := len(jsonRecords) - keep; rest > 0 {
		fmt.Fprintf(&buf, "…+%d more JSON records\n", rest)
	}
	body := bytes.TrimRight(buf.Bytes(), "\n")
	if len(body) == 0 || len(body) >= len(raw) {
		return format.Rendered{}, false
	}
	return format.Rendered{
		Body:   body,
		Note:   fmt.Sprintf("jsonl: kept %d of %d records", keep, len(jsonRecords)),
		Elided: len(jsonRecords) > keep,
	}, true
}
