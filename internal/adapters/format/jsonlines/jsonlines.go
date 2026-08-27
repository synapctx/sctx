// Package jsonlines detects and safely bounds JSON Lines / NDJSON streams.
package jsonlines

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

const (
	MinRecords  = 8
	KeepRecords = 5
	TailRecords = 2
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
		if isStructuredRecord(trimmed) && jsontext.Value([]byte(trimmed)).IsValid() {
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

// Render compacts the kept records without altering their JSON values and
// replaces the remainder with one exact record-count marker.
//
// HEAD AND TAIL, not head alone. An NDJSON stream is most often a log, and a log
// puts the thing that went wrong at the END — the summary line, the failure, the
// final state. Keeping only the opening records answered "how does this stream
// start", which is rarely the question, and did it while printing a count that
// made the omission look accounted for. The tail costs two records and is the
// half a reader usually needs.
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

	// Eliding a single record to print a marker in its place trades data for
	// nothing, so the elision has to be worth its own line before it happens.
	head, tail := KeepRecords, TailRecords
	if len(jsonRecords)-(head+tail) < 2 {
		head, tail = len(jsonRecords), 0
	}
	var buf bytes.Buffer
	compactInto := func(record string) bool {
		// v2 spells Compact as an in-place method on the value rather than a
		// dst/src function.
		compact := jsontext.Value(record)
		if err := compact.Compact(); err != nil {
			return false
		}
		buf.Write(compact)
		buf.WriteByte('\n')
		return true
	}
	for i := 0; i < head; i++ {
		if !compactInto(jsonRecords[i]) {
			return format.Rendered{}, false
		}
	}
	keep := head + tail
	if rest := len(jsonRecords) - keep; rest > 0 {
		fmt.Fprintf(&buf, "…+%d more JSON records\n", rest)
	}
	for i := len(jsonRecords) - tail; i < len(jsonRecords); i++ {
		if !compactInto(jsonRecords[i]) {
			return format.Rendered{}, false
		}
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
