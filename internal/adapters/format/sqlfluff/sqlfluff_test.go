package sqlfluff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRealFixtures(t *testing.T) {
	tests := []struct {
		name, fixture string
		argv          []string
		keep, drop    []string
	}{
		{"lint keeps every violation with location rule and full message", "sqlfluff_multi",
			[]string{"sqlfluff", "lint", "sql", "--dialect", "ansi"},
			[]string{"sql/a.sql FAIL (7)", "L1:1 LT09 Select targets should be on a new line unless there is only one select target.",
				"L1:21 LT14 The 'where' keyword should always start a new line.", "sql/b.sql FAIL (2)",
				"L1:8 AM04", "Found '  '.", "sql/d.sql FAIL (1)", "PRS Line 1, Position 1: Found unparsable section",
				"WARNING: Parsing errors found"},
			[]string{"[layout.spacing]", "P:"}},
		{"fix keeps outcome lines", "sqlfluff_fix",
			[]string{"sqlfluff", "fix", "sql/a.sql", "sql/b.sql", "--dialect", "ansi", "--force"},
			[]string{"sql/a.sql FIXED", "8 fixable linting violations found", "1 unfixable linting violations found",
				"The -f/--force option is deprecated"}, nil},
		{"pass", "sqlfluff_good", []string{"sqlfluff", "lint", "good.sql"}, []string{"All Finished!"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := load(t, tt.fixture)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw), ExitCode: 1})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			t.Logf("raw=%d render=%d\n%s", len(raw), len(body), body)
			for _, k := range tt.keep {
				if !strings.Contains(body, k) {
					t.Errorf("missing %q", k)
				}
			}
			for _, d := range tt.drop {
				if strings.Contains(body, d) {
					t.Errorf("unexpected %q", d)
				}
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	for _, argv := range [][]string{
		{"sqlfluff", "lint", "-f", "json", "x.sql"},
		{"sqlfluff", "lint", "--format=github-annotation", "x.sql"},
		{"sqlfluff", "parse", "x.sql"},
		{"sqlfluff", "rules"},
	} {
		_, err := New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader("== [x] FAIL\n")})
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: err = %v", argv, err)
		}
	}
}
