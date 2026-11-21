package svn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(argv []string, raw string, exit int) (format.Rendered, error) {
	return New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: exit})
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		argv []string
		keep []string
		drop []string
	}{
		{"checkout.txt", []string{"svn", "checkout", "file:///tmp/repo/trunk", "wc2"},
			[]string{"A wc2/git/ ×", "A wc2/cc/ ×", "Checked out revision 8."}, []string{"A    wc2/git/write.go"}},
		{"commit.txt", []string{"svn", "commit", "-m", "import"},
			[]string{"Adding cc/ ×", "Adding git/ ×", "Transmitting file data", "Committing transaction...", "Committed revision 2."}, []string{"Adding         git/write.go"}},
		{"add.txt", []string{"svn", "add", "git", "cc"}, []string{"A git/ ×"}, []string{"A         git/main_test.go"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(tc.argv, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body)*3 >= len(raw) {
				t.Errorf("expected >3x reduction: %d -> %d", len(raw), len(body))
			}
			if !out.Elided {
				t.Error("Elided not set")
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	cases := []struct {
		name, file string
		argv       []string
		exit       int
	}{
		{"small update", "update.txt", []string{"svn", "update"}, 0},
		{"commit failure", "commit-fail.txt", []string{"svn", "commit", "-m", "x", "nonexist"}, 1},
		{"info failure", "info-fail.txt", []string{"svn", "info", "file:///nonexistent"}, 1},
		{"status is not progress", "status.txt", []string{"svn", "status"}, 0},
		{"status on big checkout text", "checkout.txt", []string{"svn", "status"}, 0},
		{"log", "checkout.txt", []string{"svn", "log"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := run(tc.argv, fixture(t, tc.file), tc.exit); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// Conflicts and errors are signal: they must stay as individual lines even
// while the surrounding progress lines collapse.
func TestConflictsAndErrorsSurvive(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 25; i++ {
		b.WriteString("U    src/file" + string(rune('a'+i%26)) + ".go\n")
	}
	b.WriteString("C    src/conflicted.go\n")
	b.WriteString(" C   src/propconflict.go\n")
	b.WriteString("Skipped 'src/missing.go' -- Node remains in conflict\n")
	b.WriteString("Summary of conflicts:\n  Text conflicts: 1\n")
	b.WriteString("svn: E155015: One or more conflicts were produced while merging\n")
	out, err := run([]string{"svn", "update"}, b.String(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"U src/ ×25", "C    src/conflicted.go", " C   src/propconflict.go", "Skipped 'src/missing.go'", "Text conflicts: 1", "svn: E155015"} {
		if !strings.Contains(string(out.Body), k) {
			t.Errorf("lost %q:\n%s", k, out.Body)
		}
	}
}
