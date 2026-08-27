// Package jsoncompact is the content-sniff fallback formatter: when no
// argv-matched formatter claims a command but its stdout is valid JSON (e.g.
// a curl against an API), this compacts the payload instead of emitting it
// verbatim.
package jsoncompact

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"reflect"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxStringLen is the threshold above which string values are truncated in
// the aggressive tier.
const maxStringLen = 200

// maxArrayLen is the threshold above which arrays are elided in the
// aggressive tier.
const maxArrayLen = 10

// keepHomogeneous is how many leading elements are kept from a homogeneous
// (same key-set) object array before the elision marker.
const keepHomogeneous = 3

// Formatter is the content-sniff JSON compactor. It is never resolved by
// argv match; the run service passes its Descriptor explicitly when it
// detects stdout is JSON and no other formatter claimed the command.
type Formatter struct{}

// New constructs a Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor returns a label-only Match; "(json)" is never matched against a
// real argv[0].
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "(json)"}
}

// Aggressive re-marshals stdout compactly and recursively elides long
// strings and large arrays, always leaving an explicit "+N" marker so the
// agent knows data was dropped.
//
// Note: transforming through map[string]any does not preserve the INPUT key
// order. That was always true, but it used to be invisible: encoding/json v1
// sorted map keys on the way out, so the output was at least the same on every
// run. v2 marshals a map in randomized order, which turned a documented
// ordering caveat into output that CHANGES BETWEEN IDENTICAL RUNS -- the same
// kubectl command rendering differently each time, defeating diffing and any
// caching an agent does on the text.
//
// Deterministic(true) restores the sorted-key behaviour and with it the
// property that actually matters here: the same input always renders the same
// bytes. Callers that need the ORIGINAL key order should still use the relaxed
// tier, which compacts in place and never round-trips through a map.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("jsoncompact: reading stdout: %w", err)
	}
	if !jsontext.Value(raw).IsValid() {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	elided := elide(doc)

	body, err := json.Marshal(elided, json.Deterministic(true))
	if err != nil {
		return format.Rendered{}, fmt.Errorf("jsoncompact: marshaling elided doc: %w", err)
	}
	if len(body) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(body) >= len(raw) {
		// Elision bought nothing (e.g. tiny payload); don't pad, degrade.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{
		Body:   body,
		Note:   fmt.Sprintf("%s json → %s", humanBytes(len(raw)), humanBytes(len(body))),
		Elided: !reflect.DeepEqual(doc, elided),
	}, nil
}

// Relaxed strips whitespace only (json.Compact), a lossless transform. If
// the input is already effectively compact (>= 98% of raw size), the tier is
// inapplicable and the chain should degrade to verbatim.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	raw, err := io.ReadAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("jsoncompact: reading stdout: %w", err)
	}
	if !jsontext.Value(raw).IsValid() {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	compact := jsontext.Value(raw).Clone()
	if err := compact.Compact(); err != nil {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := []byte(compact)

	if len(body) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if float64(len(body)) >= 0.98*float64(len(raw)) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{
		Body: body,
		Note: fmt.Sprintf("%s json → %s (compacted)", humanBytes(len(raw)), humanBytes(len(body))),
	}, nil
}

// elide recursively applies string truncation and array elision to a
// decoded JSON value (map[string]any / []any / scalars).
func elide(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, sub := range val {
			out[k] = elide(sub)
		}
		return out
	case []any:
		return elideArray(val)
	case string:
		return elideString(val)
	default:
		return val
	}
}

// elideString truncates a string longer than maxStringLen, appending a
// count of dropped characters.
func elideString(s string) string {
	runes := []rune(s)
	if len(runes) <= maxStringLen {
		return s
	}
	kept := string(runes[:maxStringLen])
	dropped := len(runes) - maxStringLen
	return fmt.Sprintf("%s…(+%d chars)", kept, dropped)
}

// elideArray shortens arrays over maxArrayLen. Homogeneous arrays of objects
// (same key set) keep the first keepHomogeneous elements plus a marker
// naming how many more items of the same shape were dropped. Scalar/mixed
// arrays keep the first maxArrayLen elements plus a generic marker.
func elideArray(arr []any) []any {
	elided := make([]any, len(arr))
	for i, item := range arr {
		elided[i] = elide(item)
	}

	if len(elided) <= maxArrayLen {
		return elided
	}

	if shape, ok := homogeneousObjectShape(elided); ok {
		kept := elided[:keepHomogeneous]
		more := len(elided) - keepHomogeneous
		marker := fmt.Sprintf("…+%d more items (same shape)", more)
		_ = shape
		out := make([]any, 0, keepHomogeneous+1)
		out = append(out, kept...)
		out = append(out, marker)
		return out
	}

	kept := elided[:maxArrayLen]
	more := len(elided) - maxArrayLen
	marker := fmt.Sprintf("…+%d more", more)
	out := make([]any, 0, maxArrayLen+1)
	out = append(out, kept...)
	out = append(out, marker)
	return out
}

// homogeneousObjectShape reports whether every element of arr is an object
// (map[string]any) sharing the same set of keys.
func homogeneousObjectShape(arr []any) (map[string]struct{}, bool) {
	var shape map[string]struct{}
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		if shape == nil {
			shape = make(map[string]struct{}, len(obj))
			for k := range obj {
				shape[k] = struct{}{}
			}
			continue
		}
		if len(obj) != len(shape) {
			return nil, false
		}
		for k := range obj {
			if _, exists := shape[k]; !exists {
				return nil, false
			}
		}
	}
	if shape == nil {
		return nil, false
	}
	return shape, true
}

// humanBytes renders a byte count as a short KB-scale label.
func humanBytes(n int) string {
	const unit = 1024.0
	kb := float64(n) / unit
	return fmt.Sprintf("%.1fKB", kb)
}
