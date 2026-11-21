package mysqlcli

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
		raw, err := os.ReadFile(filepath.Join("testdata", "mysql84_"+name+"."+ext))
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

func input(stdout, stderr string, exit int) format.Input {
	return format.Input{Argv: []string{"mysql"}, Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
}

func TestAggressive(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contains []string
		absent   []string
		elided   bool
	}{
		{"table", []string{"id|customer_id|status|total|note\n", "1|2|pending|3.17|order note 1\n", "150|", "…+200 mysql rows", "351|", "400|"}, []string{"+----", "\n151|"}, true},
		{"table_small", []string{"id|name|country\n1|Customer 1|DE\n", "8|Customer 8|ES\n"}, []string{"+----", "| "}, false},
		{"nullcol", []string{"id|note\n", "7|NULL\n"}, nil, false},
		{"vert", []string{"*** 1. row ***\nid: 1\nname: Customer 1\nemail: user1@example.com\ncountry: DE\n", "*** 5. row ***"}, []string{"*****", "   id:"}, false},
		{"vert_big", []string{"*** 70. row ***", "…+50 mysql records", "*** 121. row ***", "*** 150. row ***"}, []string{"*** 71. row ***"}, true},
		{"warn", []string{"Warning (Code 1264): Out of range value for column 'a' at row 1\n", "a\n127\n"}, []string{"+----"}, false},
		{"multi", []string{"count(*)\n2000\n", "id|name\n1|Customer 1\n"}, []string{"+----"}, false},
		{"N", []string{"1\tpending\n", "…+100 mysql rows", "300\t"}, []string{"\n151\t"}, true},
		{"batch", []string{"id\tcustomer_id\tstatus\ttotal\tnote\n", "…+201 mysql rows", "400\t"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := load(t, tc.name)
			out, err := New().Aggressive(context.Background(), input(stdout, stderr, exit))
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(stdout)+len(stderr) {
				t.Fatalf("not smaller: %d >= %d", len(body), len(stdout)+len(stderr))
			}
			for _, w := range tc.contains {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in\n%s", w, body)
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
			if strings.Contains(body, "Using a password") || !out.FoldStderr {
				t.Error("password notice must be folded away with stderr absorbed")
			}
		})
	}
}

// Anything that is not provably a known result shape is declined, never guessed.
func TestDeclines(t *testing.T) {
	for _, name := range []string{"pipe", "longtext"} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, exit := load(t, name)
			if _, err := New().Aggressive(context.Background(), input(stdout, stderr, exit)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("ragged table", func(t *testing.T) {
		raw := "+----+\n| id |\n+----+\n|  1 |\n| 2 | 3 |\n+----+\n"
		if _, err := New().Aggressive(context.Background(), input(raw, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("multi-line vertical value", func(t *testing.T) {
		raw := "*************************** 1. row ***************************\n  id: 1\nnote: a\nb\n"
		if _, err := New().Aggressive(context.Background(), input(raw, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("err = %v", err)
		}
	})
}

// A non-zero exit never reaches the aggressive tier and the relaxed tier only
// removes the password notice: every error line survives.
func TestFailureKeepsErrorSignal(t *testing.T) {
	for _, name := range []string{"syntax", "missing", "auth", "refused"} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, exit := load(t, name)
			if exit == 0 {
				t.Fatal("fixture should be a failure")
			}
			if _, err := New().Aggressive(context.Background(), input(stdout, stderr, exit)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("aggressive err = %v", err)
			}
			out, err := New().Relaxed(context.Background(), input(stdout, stderr, exit))
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if !strings.Contains(body, "ERROR ") || strings.Contains(body, "Using a password") || !out.FoldStderr {
				t.Errorf("body = %q", body)
			}
			var want string
			for _, l := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(l, "ERROR") {
					want = l
				}
			}
			if !strings.Contains(body, want) {
				t.Errorf("error line lost: %q", body)
			}
		})
	}
}

func TestRelaxedWithoutNoticeDeclines(t *testing.T) {
	if _, err := New().Relaxed(context.Background(), input("a\tb\n", "ERROR 1 (x): y\n", 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err = %v", err)
	}
}
