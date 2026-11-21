package jsonpack

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func mustParse(t testing.TB, s string) any {
	t.Helper()
	v, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func rows(n int, f func(i int) string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = f(i)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// forcePack runs the codec without the size gates so small fixtures exercise it.
func forcePack(v any) (string, bool, error) {
	w := &writer{tables: true}
	if err := w.value(v, 0); err != nil {
		return "", false, err
	}
	return strings.TrimRight(w.b.String(), "\n"), w.packed, nil
}

func TestPackRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // exact packed form; "" means only roundtrip is checked
	}{
		{
			name: "plain table",
			in:   rows(4, func(i int) string { return fmt.Sprintf(`{"id":%d,"name":"item-%d","ok":%t}`, i, i, i%2 == 0) }),
			want: "T[4]{id,name,ok}\n0|item-0|true\n1|item-1|false\n2|item-2|true\n3|item-3|false",
		},
		{
			name: "nested objects flatten and arrays stay json",
			in: rows(3, func(i int) string {
				return fmt.Sprintf(`{"id":%d,"user":{"login":"u%d","org":{"id":%d}},"tags":["a","b"]}`, i, i, i)
			}),
			want: "T[3]{id,user.login,user.org.id,tags}\n0|u0|0|[\"a\",\"b\"]\n1|u1|1|[\"a\",\"b\"]\n2|u2|2|[\"a\",\"b\"]",
		},
		{
			name: "missing null empty are distinct",
			in:   `[{"a":1,"b":null,"c":""},{"a":2,"c":"x"},{"a":3,"b":"y","c":"z"},{"a":4,"b":"q","c":"w"}]`,
			want: "T[4]{a,b,c}\n1|null|\"\"\n2|~|x\n3|y|z\n4|q|w",
		},
		{
			name: "separators newlines and lookalikes are quoted",
			in: `[{"s":"a|b","n":"1","t":"true","u":"null","e":"~x","l":"line1\nline2","q":"\"x"},` +
				`{"s":"a|b","n":"1","t":"true","u":"null","e":"~x","l":"line1\nline2","q":"\"x"},` +
				`{"s":"a|b","n":"1","t":"true","u":"null","e":"~x","l":"line1\nline2","q":"\"x"}]`,
		},
		{
			name: "dotted keys differ from nested paths",
			in: rows(3, func(i int) string {
				return fmt.Sprintf(`{"a.b":%d,"a":{"b":%d},"k,y":1,"":2}`, i, i+10)
			}),
		},
		{
			name: "numbers keep exact text",
			in:   `[{"n":1.0},{"n":1e5},{"n":-0},{"n":12345678901234567890123},{"n":0.1000}]`,
			want: "T[5]{n}\n1.0\n1e5\n-0\n12345678901234567890123\n0.1000",
		},
		{
			name: "table nested under an object key",
			in: `{"total":3,"items":` + rows(3, func(i int) string {
				return fmt.Sprintf(`{"id":%d,"name":"nm%d"}`, i, i)
			}) + `,"next":null}`,
		},
		{
			name: "empty object leaf and unicode",
			in:   `[{"a":{},"s":"héllo ☃"},{"a":{},"s":"héllo ☃"},{"a":{"x":1},"s":"héllo ☃"}]`,
		},
		{
			name: "leading and trailing spaces",
			in:   `[{"a":" x"},{"a":"y "},{"a":" "}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := mustParse(t, tt.in)
			out, packed, err := forcePack(v)
			if err != nil {
				t.Fatal(err)
			}
			if !packed {
				t.Fatal("nothing was packed")
			}
			got, err := Unpack(out)
			if err != nil {
				t.Fatalf("Unpack(%q): %v", out, err)
			}
			if !Equal(got, v) {
				t.Fatalf("roundtrip mismatch\npacked: %q", out)
			}
			if tt.want != "" && out != tt.want {
				t.Fatalf("packed form\n got: %q\nwant: %q", out, tt.want)
			}
		})
	}
}

func TestPackGates(t *testing.T) {
	big := rows(50, func(i int) string {
		return fmt.Sprintf(`{"id":%d,"name":"item%d","status":"open","labels":["x"]}`, i, i)
	})
	if s, ok := Pack(mustParse(t, big)); !ok || len(s) >= len(big) {
		t.Fatalf("expected a smaller pack, ok=%v len=%d raw=%d", ok, len(s), len(big))
	}
	for name, in := range map[string]string{
		"too few rows":  `[{"a":1},{"a":2}]`,
		"scalars":       `[1,2,3,4,5]`,
		"mixed array":   `[{"a":1},2,{"a":3},{"a":4}]`,
		"no arrays":     `{"a":{"b":1}}`,
		"not smaller":   `[{"a":1},{"b":2},{"c":3},{"d":4}]`,
		"empty objects": `[{},{},{},{}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if s, ok := Pack(mustParse(t, in)); ok {
				t.Fatalf("unexpected pack: %q", s)
			}
		})
	}
}

func TestPackRejectsFloat(t *testing.T) {
	if _, ok := Pack([]any{1.5}); ok {
		t.Fatal("float64 must be unsupported: it cannot be exact")
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{`{"a":1,"a":2}`, `[1,2`, `1 2`, ``, `nope`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) succeeded", in)
		}
	}
}

func TestUnpackRejectsMalformed(t *testing.T) {
	for _, in := range []string{
		"T[2]{a}\n1\n", "T[1]{a,b}\n1\n", "T[1]{a}\n\n", "T[0]{a}\n", "T[1]{a}\n\"x\ny\n",
		"T[1]{a}\n~x\n", "T[99999]{a}\n1\n", "T[1]{}\n", "{\"a\":1,\"a\":2}", "bare", "[1,",
	} {
		if _, err := Unpack(in); err == nil {
			t.Errorf("Unpack(%q) succeeded", in)
		}
	}
}

func TestInconsistentTreeIsRefused(t *testing.T) {
	v := []any{&Object{Keys: []string{"a"}, Vals: map[string]any{}}}
	if _, err := pack(v); err == nil || errors.Is(err, errRoundtrip) {
		t.Fatalf("want a clean refusal, got %v", err)
	}
}

func FuzzRoundtrip(f *testing.F) {
	f.Add(rows(5, func(i int) string { return fmt.Sprintf(`{"id":%d,"name":"n%d","t":["a","b"],"o":{"x":%d}}`, i, i, i) }))
	f.Add(`[{"a":1,"b":null,"c":""},{"a":2,"c":"x|y"},{"a":3,"b":"~","c":"\n"},{"a":"1","b":"true"}]`)
	f.Add(`{"items":[{"a.b":1,"a":{"b":2}},{"a.b":1.50,"a":{"b":-0}},{"":1e9,"k":"[1]"},{"k":"{}"}],"n":1}`)
	f.Add(`[[{"a":"x"},{"a":"y"},{"a":"z"}],[{"a":"x"},{"a":"y"},{"a":"z"}]]`)
	f.Add(`[{"s":"\"q\"|\\"},{"s":"é "},{"s":" "},{"s":"T[1]{a}"}]`)
	f.Fuzz(func(t *testing.T, in string) {
		v, err := Parse([]byte(in))
		if err != nil {
			return
		}
		out, _, err := forcePack(v)
		if err != nil {
			return
		}
		got, err := Unpack(out)
		if err != nil {
			t.Fatalf("Unpack of own output failed: %v\ninput: %q\npacked: %q", err, in, out)
		}
		if !Equal(got, v) {
			t.Fatalf("roundtrip mismatch\ninput: %q\npacked: %q", in, out)
		}
		if _, err := pack(v); errors.Is(err, errRoundtrip) {
			t.Fatalf("pack self-check failed: %v\ninput: %q", err, in)
		}
	})
}
