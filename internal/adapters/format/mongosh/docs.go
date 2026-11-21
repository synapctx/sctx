package mongosh

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/synapctx/sctx/internal/domain/format"
)

// A query result is the answer the agent asked for, so documents are only
// whitespace-collapsed (lossless) up to documentCap. Above that, the first
// docHead and last docTail documents are kept and the rest are counted in an
// explicit "…+N more documents" marker.
const (
	documentCap = 200
	docHead     = 150
	docTail     = 50
)

// compactDocumentArray handles mongosh's default shell-object output (NOT
// valid JSON: ObjectId('...'), ISODate('...'), Long('...'), etc.), which
// typically looks like `[ { ... }, { ... }, ... ]`. Each document is
// collapsed to a single line; above documentCap the middle is replaced with a
// "…+N more documents" marker. If the input isn't
// confidently a top-level array of `{...}` documents, it returns
// ErrTierInapplicable so the chain degrades to the relaxed tier.
func compactDocumentArray(trimmed []byte) (format.Rendered, error) {
	s := strings.TrimSpace(string(trimmed))
	elems, ok := splitTopLevelArray(s)
	if !ok || len(elems) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	for _, e := range elems {
		if !strings.HasPrefix(strings.TrimSpace(e), "{") {
			// Not confidently a document array (mixed/scalar array) —
			// degrade rather than guess.
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}

	kept := elems
	more := 0
	var tail []string
	if len(elems) > documentCap {
		kept = elems[:docHead]
		tail = elems[len(elems)-docTail:]
		more = len(elems) - docHead - docTail
	}

	var b strings.Builder
	b.WriteString("[\n")
	for i, e := range kept {
		b.WriteString("  ")
		b.WriteString(collapseWhitespace(e))
		if i != len(kept)-1 || more > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "  …+%d more documents\n", more)
		for i, e := range tail {
			b.WriteString("  ")
			b.WriteString(collapseWhitespace(e))
			if i != len(tail)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("]")

	body := b.String()
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:   []byte(body),
		Note:   fmt.Sprintf("mongosh %d documents → %d shown", len(elems), len(elems)-more),
		Elided: more > 0,
	}, nil
}

// compactDocument collapses a single top-level `{...}` value (findOne, stats,
// explain, ...) to one line. It returns ErrTierInapplicable unless the text is
// provably exactly one balanced document.
func compactDocument(trimmed []byte) (format.Rendered, error) {
	s := strings.TrimSpace(string(trimmed))
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if elems, ok := splitTopLevelArray("[" + s + "]"); !ok || len(elems) != 1 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(collapseWhitespace(s) + "\n"), Note: "mongosh document collapsed"}, nil
}

// splitTopLevelArray splits s (expected to be `[...]`) into its top-level
// elements, respecting nested {}/[]/() and quoted strings so commas inside
// a document or a string value never split an element. ok is false if s
// isn't bracketed as an array at all.
func splitTopLevelArray(s string) (elems []string, ok bool) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, false
	}
	inner := s[1 : len(s)-1]

	var (
		depth int
		cur   strings.Builder
		inStr bool
		quote rune
		esc   bool
	)
	flush := func() {
		e := strings.TrimSpace(cur.String())
		if e != "" {
			elems = append(elems, e)
		}
		cur.Reset()
	}

	for _, r := range inner {
		if inStr {
			cur.WriteRune(r)
			switch {
			case esc:
				esc = false
			case r == '\\':
				esc = true
			case r == quote:
				inStr = false
			}
			continue
		}
		switch r {
		case '\'', '"':
			inStr = true
			quote = r
			cur.WriteRune(r)
		case '{', '[', '(':
			depth++
			cur.WriteRune(r)
		case '}', ']', ')':
			depth--
			cur.WriteRune(r)
		case ',':
			if depth == 0 {
				flush()
			} else {
				cur.WriteRune(r)
			}
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return elems, true
}

// collapseWhitespace flattens a multi-line document into a single compact
// line: runs of whitespace outside quoted strings collapse to a single
// space; whitespace inside quoted strings is left untouched.
func collapseWhitespace(s string) string {
	var b strings.Builder
	inStr := false
	quote := rune(0)
	esc := false
	lastWasSpace := false

	for _, r := range s {
		if inStr {
			b.WriteRune(r)
			switch {
			case esc:
				esc = false
			case r == '\\':
				esc = true
			case r == quote:
				inStr = false
			}
			lastWasSpace = false
			continue
		}
		if r == '\'' || r == '"' {
			inStr = true
			quote = r
			b.WriteRune(r)
			lastWasSpace = false
			continue
		}
		if unicode.IsSpace(r) {
			if !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastWasSpace = false
	}
	return strings.TrimSpace(b.String())
}
