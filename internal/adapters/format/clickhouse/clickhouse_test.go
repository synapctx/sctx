package clickhouse

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

func read(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ch253_"+name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func aggressive(t *testing.T, name string) (format.Rendered, string, error) {
	t.Helper()
	raw := read(t, name+".stdout")
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"clickhouse-client", "--query", "q"}, Stdout: strings.NewReader(raw)})
	return out, raw, err
}

func TestPrettyFormatsCompactAndCap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contains []string
		absent   []string
	}{
		{"pc230", []string{"id|customer_id|status|total|note\n", "1|1|paid|3.17|order note 1\n", "7|7|failed|22.189999999999998|ᴺᵁᴸᴸ\n", "…+30 clickhouse rows", "230|"}, []string{"┌", "│", "└", "\n151|"}},
		{"p230", []string{"id|status\n", "1|paid\n", "…+30 clickhouse rows", "230|pending"}, []string{"┏", "├", "│"}},
		{"pretty", []string{"id|customer_id|status|total|note\n", "8|8|refunded|25.36|order note 8"}, []string{"┌", "…+"}},
		{"pretty_default", []string{"id|customer_id|status|total|note\n", "4|4|refunded|12.68|order note 4"}, []string{"┏", "…+"}},
		{"tsv300", []string{"1\tpaid\n", "…+100 clickhouse rows", "300\t"}, []string{"\n151\t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, raw, err := aggressive(t, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Fatal("not smaller")
			}
			for _, w := range tc.contains {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in\n%.500s", w, body)
				}
			}
			for _, w := range tc.absent {
				if strings.Contains(body, w) {
					t.Errorf("unexpected %q", w)
				}
			}
			if out.Elided != strings.Contains(body, "…+") {
				t.Errorf("Elided = %v", out.Elided)
			}
		})
	}
}

func TestOtherShapesDecline(t *testing.T) {
	for _, name := range []string{"vert", "csv"} {
		if _, _, err := aggressive(t, name); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestFailuresKeepCodeMessageAndErrorName(t *testing.T) {
	for _, name := range []string{"syntax", "missing", "auth", "refused"} {
		t.Run(name, func(t *testing.T) {
			stderr := read(t, name+".stderr")
			exit, _ := strconv.Atoi(strings.TrimSpace(read(t, name+".exit")))
			in := func() format.Input {
				return format.Input{Stdout: strings.NewReader(""), Stderr: strings.NewReader(stderr), ExitCode: exit}
			}
			if _, err := New().Aggressive(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("aggressive err = %v", err)
			}
			out, err := New().Relaxed(context.Background(), in())
			if name != "syntax" {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("short errors must pass through verbatim: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			for _, w := range []string{"Code: 62. DB::Exception: Syntax error: failed at position 1 (selec): selec 1. Expected one of: Query, Query with output, EXPLAIN,", "expected tokens", "(SYNTAX_ERROR)"} {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in %q", w, body)
				}
			}
			if len(body) > len(stderr)/3 || !out.Elided || !out.FoldStderr {
				t.Errorf("len=%d raw=%d elided=%v fold=%v", len(body), len(stderr), out.Elided, out.FoldStderr)
			}
		})
	}
}

func TestInteractive(t *testing.T) {
	for argv, want := range map[string]bool{
		"clickhouse-client":                             true,
		"clickhouse-client --host db --user u":          true,
		"clickhouse-client --query select_1":            false,
		"clickhouse-client -q x":                        false,
		"clickhouse-client --queries-file q.sql":        false,
		"clickhouse-client --host h --query=select_now": false,
		"clickhouse-client --version":                   false,
	} {
		if got := Interactive(strings.Fields(argv)); got != want {
			t.Errorf("Interactive(%q) = %v, want %v", argv, got, want)
		}
	}
}
