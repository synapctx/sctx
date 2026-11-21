package sqlite3

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) (stdout, stderr string, exit int) {
	t.Helper()
	read := func(ext string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", "sqlite354_"+name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		t.Fatal(err)
	}
	return read("stdout"), read("stderr"), exit
}

func run(t *testing.T, name string, argv ...string) (format.Rendered, string, error) {
	t.Helper()
	stdout, stderr, exit := load(t, name)
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: append([]string{"sqlite3"}, argv...), Stdout: strings.NewReader(stdout),
		Stderr: strings.NewReader(stderr), ExitCode: exit,
	})
	return out, stdout, err
}

func TestRealSqlite3Shapes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argv     []string
		contains []string
		absent   []string
		elided   bool
	}{
		{"box", []string{"-box"}, []string{"id|customer_id|status|total|note\n", "1|2|pending|3.17|order note 1\n", "230|"}, []string{"╭", "│", "\n151|"}, true},
		{"table", []string{"-table"}, []string{"id|customer_id|status|total|note\n", "…+30 sqlite3 rows", "7|8|refunded|22.189999999999998|\n"}, []string{"+----", "| "}, true},
		{"md", []string{"-markdown"}, []string{"id|customer_id|status|total|note\n", "…+30 sqlite3 rows"}, []string{"|--"}, true},
		{"column", []string{"-column", "-header"}, []string{"id|customer_id|status|total|note\n", "1|2|pending|3.17|order note 1\n", "…+30 sqlite3 rows", "230|"}, []string{"---  ---"}, true},
		{"small_box", []string{"-box"}, []string{"id|name|country\n", "6|Customer 6|DE"}, []string{"╭"}, false},
		{"small_col", []string{"-column", "-header"}, []string{"id|name|country\n", "1|Customer 1|DE\n"}, []string{"--  ---"}, false},
		{"agg", []string{"-column", "-header"}, []string{"status|count(*)|sum(total)\n"}, nil, false},
		{"list300", nil, []string{"1|pending\n", "…+100 sqlite3 rows", "300|"}, []string{"\n151|"}, true},
		{"csv", []string{"-csv", "-header"}, []string{"id,customer_id,status,total,note\n", "…+31 sqlite3 rows"}, nil, true},
		{"line150", []string{"-line"}, []string{"id: 1\nname: Customer 1\n", "…+50 sqlite3 records", "id: 150\n"}, []string{"   name:"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, raw, err := run(t, tc.name, tc.argv...)
			if err != nil {
				t.Fatalf("declined: %v", err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Fatalf("not smaller")
			}
			for _, w := range tc.contains {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in\n%s", w, head(body))
				}
			}
			for _, w := range tc.absent {
				if strings.Contains(body, w) {
					t.Errorf("unexpected %q", w)
				}
			}
			if out.Elided != tc.elided {
				t.Errorf("Elided = %v, want %v", out.Elided, tc.elided)
			}
		})
	}
}

func head(s string) string {
	if len(s) > 600 {
		return s[:600]
	}
	return s
}

// Shapes that are dot-command text, JSON, empty, or failures must be declined.
func TestDeclines(t *testing.T) {
	for _, name := range []string{"json", "tables", "schema", "schema1", "pragma", "empty", "syntax", "missing", "nodb"} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := run(t, name); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("csv with a quoted field spanning lines", func(t *testing.T) {
		raw := strings.Repeat("\"a,\nb\",1\n", 300)
		_, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"sqlite3", "-csv"}, Stdout: strings.NewReader(raw)})
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("schema text above the cap is never capped", func(t *testing.T) {
		raw := strings.Repeat("CREATE TABLE t(a integer, b text);\n", 300)
		_, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"sqlite3", "x.db", ".schema"}, Stdout: strings.NewReader(raw)})
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRelaxedAlwaysDeclines(t *testing.T) {
	if _, err := New().Relaxed(context.Background(), format.Input{}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatal(err)
	}
}
