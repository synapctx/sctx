// Package jsonpack losslessly packs JSON arrays of objects into a compact
// tabular form: one schema header, then one line per object.
//
// It is the lossless counterpart to jsoncompact's sampling. The property that
// makes it safe to put in front of an agent is enforced in code, not argued in
// prose: Pack DECODES ITS OWN OUTPUT with Unpack and requires exact semantic
// equality with its input before returning anything. A packing bug therefore
// degrades to "not packed" rather than to wrong data.
//
// # Grammar
//
// A packed document is compact JSON in which any array may be replaced by a
// table. JSON values never begin with 'T', so a table is unambiguous at any
// value position:
//
//	table  = "T[" N "]{" col *("," col) "}" LF row *(row)     ; exactly N rows
//	col    = seg *("." seg)                                   ; a key path
//	seg    = 1*( ALPHA / DIGIT / "_" / "-" ) / JSON-string    ; quoted when not bare-safe
//	row    = cell *("|" cell) LF                              ; one cell per col
//	cell   = "~"                                              ; key absent in this object
//	       / JSON-string / JSON-array / JSON-object           ; compact JSON, no tables
//	       / bare                                             ; see below
//
// A bare cell runs to the next '|' or LF and is read as: true, false, null, a
// JSON number (its text is kept exactly, so 1.0, 1e5 and -0 survive), or else
// a string. The encoder writes a string bare only when that reading gives the
// same string back: non-empty, no control characters, no '|', not starting
// with '"', '[', '{' or '~', and not itself a number or literal. Everything
// else is written as a JSON string. Consequences worth stating:
//
//   - "" is written "" (empty), null is null, an absent key is ~ — three
//     distinct cells.
//   - A '|' or newline inside a value can never split a cell: such strings are
//     quoted, and quoted/array/object cells are consumed by a real JSON parser.
//   - Nested non-empty objects are flattened to dotted columns (a.b.c) up to
//     three levels; arrays and empty objects stay as JSON cells. A key that
//     itself contains a dot is a quoted segment ("a.b"), distinct from the path
//     a -> b.
//
// Equality is semantic: object key ORDER is not preserved across a packed
// table (columns follow first appearance), and numbers compare by their exact
// text. Duplicate object keys are rejected by Parse.
package jsonpack

import (
	"bytes"
	stdjson "encoding/json"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// MinRows is the fewest objects an array needs before it is considered.
	MinRows = 3
	// maxCols bounds the table width; wider arrays are left as JSON.
	maxCols = 256
	// maxFlatten is how many object levels are flattened into dotted columns.
	maxFlatten = 3
	// packDepth and unpackDepth bound nesting. Unpack is deliberately more
	// permissive than Pack so anything Pack writes can be read back.
	packDepth   = 200
	unpackDepth = 1000
	// tableSavingPct: a single array is packed only when its table is at most
	// this percentage of its compact-JSON size.
	tableSavingPct = 90
	// docSavingPct: Pack returns a result only when the whole document is at
	// most this percentage of its compact-JSON size (a >=15% saving).
	docSavingPct = 85
)

var (
	errUnsupported = errors.New("jsonpack: unsupported value")
	errTooWide     = errors.New("jsonpack: too many columns")
	errNothing     = errors.New("jsonpack: nothing packed")
	errNotSmaller  = errors.New("jsonpack: not meaningfully smaller")
	errRoundtrip   = errors.New("jsonpack: roundtrip mismatch")
)

// Object is a JSON object that remembers key order.
type Object struct {
	Keys []string
	Vals map[string]any
}

// Set stores v under k, appending k to Keys when it is new.
func (o *Object) Set(k string, v any) {
	if o.Vals == nil {
		o.Vals = make(map[string]any)
	}
	if _, ok := o.Vals[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Vals[k] = v
}

// Parse decodes JSON into the tree Pack accepts: *Object, []any, string,
// json.Number (exact text), bool and nil.
func Parse(raw []byte) (any, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	v, err := parseTok(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("jsonpack: trailing data after JSON value")
	}
	return v, nil
}

func parseTok(dec *jsontext.Decoder, depth int) (any, error) {
	if depth > packDepth {
		return nil, errUnsupported
	}
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		obj := &Object{}
		for dec.PeekKind() != '}' {
			kt, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			key := kt.String() // a Token is voided by the next decoder call
			v, err := parseTok(dec, depth+1)
			if err != nil {
				return nil, err
			}
			obj.Set(key, v)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return obj, nil
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		arr := []any{}
		for dec.PeekKind() != ']' {
			v, err := parseTok(dec, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return arr, nil
	case '"':
		t, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		return t.String(), nil
	case '0':
		v, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		return stdjson.Number(string(v)), nil
	case 't', 'f':
		t, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		return t.Bool(), nil
	case 'n':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	_, err := dec.ReadToken()
	if err == nil {
		err = errUnsupported
	}
	return nil, err
}

// Pack returns the packed form of v and true, or "", false when v has no
// packable array, packing is not meaningfully smaller than compact JSON, or
// the roundtrip self-check fails.
func Pack(v any) (string, bool) {
	s, err := pack(v)
	return s, err == nil
}

func pack(v any) (string, error) {
	w := &writer{tables: true}
	if err := w.value(v, 0); err != nil {
		return "", err
	}
	if !w.packed {
		return "", errNothing
	}
	out := strings.TrimRight(w.b.String(), "\n")

	c := &writer{}
	if err := c.value(v, 0); err != nil {
		return "", err
	}
	if len(out)*100 > c.b.Len()*docSavingPct {
		return "", errNotSmaller
	}

	got, err := Unpack(out)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errRoundtrip, err)
	}
	if !Equal(got, v) {
		return "", errRoundtrip
	}
	return out, nil
}

// Equal reports semantic equality: object key order is ignored, numbers
// compare by exact text.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case stdjson.Number:
		y, ok := b.(stdjson.Number)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *Object:
		y, ok := b.(*Object)
		if !ok || x == nil || y == nil || len(x.Vals) != len(y.Vals) {
			return false
		}
		for k, xv := range x.Vals {
			yv, ok := y.Vals[k]
			if !ok || !Equal(xv, yv) {
				return false
			}
		}
		return true
	}
	return false
}

// writer emits compact JSON, optionally replacing arrays with tables.
type writer struct {
	b      strings.Builder
	tables bool
	packed bool
}

func (w *writer) value(v any, depth int) error {
	if depth > packDepth {
		return errUnsupported
	}
	switch x := v.(type) {
	case nil:
		w.b.WriteString("null")
	case bool:
		w.b.WriteString(strconv.FormatBool(x))
	case string:
		q, err := quote(x)
		if err != nil {
			return err
		}
		w.b.WriteString(q)
	case stdjson.Number:
		if !validNumber(string(x)) {
			return errUnsupported
		}
		w.b.WriteString(string(x))
	case *Object:
		if x == nil || len(x.Keys) != len(x.Vals) {
			return errUnsupported
		}
		w.b.WriteByte('{')
		for i, k := range x.Keys {
			val, ok := x.Vals[k]
			if !ok {
				return errUnsupported
			}
			q, err := quote(k)
			if err != nil {
				return err
			}
			if i > 0 {
				w.b.WriteByte(',')
			}
			w.b.WriteString(q)
			w.b.WriteByte(':')
			if err := w.value(val, depth+1); err != nil {
				return err
			}
		}
		w.b.WriteByte('}')
	case []any:
		if w.tables {
			t, ok, err := buildTable(x, depth)
			if err != nil {
				return err
			}
			if ok {
				w.b.WriteString(t)
				w.packed = true
				return nil
			}
		}
		w.b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				w.b.WriteByte(',')
			}
			if err := w.value(e, depth+1); err != nil {
				return err
			}
		}
		w.b.WriteByte(']')
	default:
		return errUnsupported
	}
	return nil
}

func quote(s string) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", errUnsupported
	}
	return string(b), nil
}

// validNumber reports whether s is a JSON number token with no surrounding
// whitespace.
func validNumber(s string) bool {
	if s == "" || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune("0123456789+-.eE", rune(s[i])) {
			return false
		}
	}
	return jsontext.Value(s).IsValid()
}

// buildTable returns the table for arr, or ok=false when arr is not worth or
// not able to be a table.
func buildTable(arr []any, depth int) (string, bool, error) {
	if len(arr) < MinRows {
		return "", false, nil
	}
	rows := make([]*Object, len(arr))
	for i, e := range arr {
		o, ok := e.(*Object)
		if !ok || o == nil {
			return "", false, nil
		}
		rows[i] = o
	}

	index := map[string]int{}
	var cols []string
	cells := make([]map[int]string, len(rows))
	for r, o := range rows {
		cells[r] = map[int]string{}
		err := flatten(o, nil, 0, func(path []string, val any) error {
			spec, err := specOf(path)
			if err != nil {
				return err
			}
			j, seen := index[spec]
			if !seen {
				if len(cols) >= maxCols {
					return errTooWide
				}
				j = len(cols)
				index[spec] = j
				cols = append(cols, spec)
			}
			c, err := cellOf(val, depth)
			if err != nil {
				return err
			}
			cells[r][j] = c
			return nil
		})
		if errors.Is(err, errTooWide) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
	}
	if len(cols) == 0 {
		return "", false, nil
	}

	var t strings.Builder
	t.WriteString("T[" + strconv.Itoa(len(rows)) + "]{" + strings.Join(cols, ",") + "}\n")
	for r := range rows {
		for j := range cols {
			if j > 0 {
				t.WriteByte('|')
			}
			if c, ok := cells[r][j]; ok {
				t.WriteString(c)
			} else {
				t.WriteByte('~')
			}
		}
		t.WriteByte('\n')
	}

	cw := &writer{}
	if err := cw.value(arr, depth); err != nil {
		return "", false, err
	}
	if t.Len()*100 > cw.b.Len()*tableSavingPct {
		return "", false, nil
	}
	return t.String(), true, nil
}

// flatten visits the leaves of o. A leaf is anything but a non-empty object
// within maxFlatten levels.
func flatten(o *Object, prefix []string, level int, visit func([]string, any) error) error {
	if len(o.Keys) != len(o.Vals) {
		return errUnsupported
	}
	for _, k := range o.Keys {
		v, ok := o.Vals[k]
		if !ok {
			return errUnsupported
		}
		path := append(append([]string(nil), prefix...), k)
		if sub, isObj := v.(*Object); isObj && sub != nil && len(sub.Keys) > 0 && level < maxFlatten {
			if err := flatten(sub, path, level+1, visit); err != nil {
				return err
			}
			continue
		}
		if err := visit(path, v); err != nil {
			return err
		}
	}
	return nil
}

func isBareSeg(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func specOf(path []string) (string, error) {
	segs := make([]string, len(path))
	for i, s := range path {
		if isBareSeg(s) {
			segs[i] = s
			continue
		}
		q, err := quote(s)
		if err != nil {
			return "", err
		}
		segs[i] = q
	}
	return strings.Join(segs, "."), nil
}

// classify reads a bare token: a literal, a number, or else a string.
func classify(tok string) any {
	switch tok {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if validNumber(tok) {
		return stdjson.Number(tok)
	}
	return tok
}

func bareSafe(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '"', '[', '{', '~':
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == '|' {
			return false
		}
	}
	_, isStr := classify(s).(string)
	return isStr
}

func cellOf(v any, depth int) (string, error) {
	if s, ok := v.(string); ok && bareSafe(s) {
		return s, nil
	}
	w := &writer{}
	if err := w.value(v, depth+2); err != nil {
		return "", err
	}
	return w.b.String(), nil
}

// Unpack parses the packed grammar back into the Parse tree shape.
func Unpack(s string) (any, error) {
	p := &parser{s: s}
	v, err := p.value(0, true)
	if err != nil {
		return nil, err
	}
	if strings.Trim(s[p.i:], "\n") != "" {
		return nil, p.errf("trailing data")
	}
	return v, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) errf(msg string) error {
	return fmt.Errorf("jsonpack: %s at offset %d", msg, p.i)
}

func (p *parser) value(depth int, tables bool) (any, error) {
	if depth > unpackDepth {
		return nil, p.errf("nesting too deep")
	}
	if p.i >= len(p.s) {
		return nil, p.errf("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		return p.str()
	case c == 'T' && tables:
		return p.table(depth)
	}
	j := p.i
	for j < len(p.s) && !strings.ContainsRune(",]}\n", rune(p.s[j])) {
		j++
	}
	v := classify(p.s[p.i:j])
	if _, isStr := v.(string); isStr {
		return nil, p.errf("invalid value")
	}
	p.i = j
	return v, nil
}

func (p *parser) str() (string, error) {
	j := p.i + 1
	for j < len(p.s) && p.s[j] != '"' {
		if p.s[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(p.s) {
		return "", p.errf("unterminated string")
	}
	var out string
	if err := json.Unmarshal([]byte(p.s[p.i:j+1]), &out); err != nil {
		return "", p.errf("invalid string")
	}
	p.i = j + 1
	return out, nil
}

func (p *parser) peek(c byte) bool { return p.i < len(p.s) && p.s[p.i] == c }

func (p *parser) expect(c byte) error {
	if !p.peek(c) {
		return p.errf("expected " + strconv.QuoteRune(rune(c)))
	}
	p.i++
	return nil
}

func (p *parser) object(depth int) (any, error) {
	p.i++
	obj := &Object{}
	if p.peek('}') {
		p.i++
		return obj, nil
	}
	for {
		if !p.peek('"') {
			return nil, p.errf("expected key")
		}
		k, err := p.str()
		if err != nil {
			return nil, err
		}
		if _, dup := obj.Vals[k]; dup {
			return nil, p.errf("duplicate key")
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		v, err := p.value(depth+1, true)
		if err != nil {
			return nil, err
		}
		obj.Set(k, v)
		if p.peek(',') {
			p.i++
			continue
		}
		return obj, p.expect('}')
	}
}

func (p *parser) array(depth int) (any, error) {
	p.i++
	arr := []any{}
	if p.peek(']') {
		p.i++
		return arr, nil
	}
	for {
		v, err := p.value(depth+1, true)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
		if p.peek(',') {
			p.i++
			continue
		}
		return arr, p.expect(']')
	}
}

func (p *parser) table(depth int) (any, error) {
	p.i++
	if err := p.expect('['); err != nil {
		return nil, err
	}
	j := p.i
	for j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
		j++
	}
	n, err := strconv.Atoi(p.s[p.i:j])
	if err != nil || n < 1 || n > len(p.s) {
		return nil, p.errf("invalid row count")
	}
	p.i = j
	if err := p.expect(']'); err != nil {
		return nil, err
	}
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	var cols [][]string
	for {
		var path []string
		for {
			seg, err := p.seg()
			if err != nil {
				return nil, err
			}
			path = append(path, seg)
			if p.peek('.') {
				p.i++
				continue
			}
			break
		}
		cols = append(cols, path)
		if p.peek(',') {
			p.i++
			continue
		}
		break
	}
	if err := p.expect('}'); err != nil {
		return nil, err
	}
	if err := p.expect('\n'); err != nil {
		return nil, err
	}

	rows := make([]any, 0, n)
	for r := 0; r < n; r++ {
		obj := &Object{}
		for c, path := range cols {
			v, missing, err := p.cell(depth)
			if err != nil {
				return nil, err
			}
			if !missing {
				if err := setPath(obj, path, v); err != nil {
					return nil, p.errf(err.Error())
				}
			}
			if c < len(cols)-1 {
				err = p.expect('|')
			} else if p.i < len(p.s) {
				err = p.expect('\n')
			}
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, obj)
	}
	return rows, nil
}

func (p *parser) seg() (string, error) {
	if p.peek('"') {
		return p.str()
	}
	j := p.i
	for j < len(p.s) && isBareSeg(p.s[j:j+1]) {
		j++
	}
	if j == p.i {
		return "", p.errf("expected column name")
	}
	seg := p.s[p.i:j]
	p.i = j
	return seg, nil
}

func (p *parser) cell(depth int) (v any, missing bool, err error) {
	if p.i >= len(p.s) {
		return nil, false, p.errf("unexpected end in row")
	}
	switch p.s[p.i] {
	case '"', '[', '{':
		v, err = p.value(depth+1, false)
		return v, false, err
	case '~':
		p.i++
		if p.i < len(p.s) && p.s[p.i] != '|' && p.s[p.i] != '\n' {
			return nil, false, p.errf("invalid cell")
		}
		return nil, true, nil
	}
	j := p.i
	for j < len(p.s) && p.s[j] != '|' && p.s[j] != '\n' {
		j++
	}
	tok := p.s[p.i:j]
	if tok == "" {
		return nil, false, p.errf("empty cell")
	}
	p.i = j
	return classify(tok), false, nil
}

func setPath(o *Object, path []string, v any) error {
	for _, k := range path[:len(path)-1] {
		next, ok := o.Vals[k]
		if !ok {
			sub := &Object{}
			o.Set(k, sub)
			o = sub
			continue
		}
		sub, isObj := next.(*Object)
		if !isObj {
			return errors.New("column path collides with a value")
		}
		o = sub
	}
	last := path[len(path)-1]
	if _, exists := o.Vals[last]; exists {
		return errors.New("column set twice")
	}
	o.Set(last, v)
	return nil
}
