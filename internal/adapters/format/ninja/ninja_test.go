package ninja

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

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		exit int
		keep []string
		drop []string
	}{
		{"build-fail.txt", 1,
			[]string{"FAILED: [code=1] CMakeFiles/b.dir/src/b.c.o", "b.c:1:24: error: use of undeclared identifier 'nope'", "1 error generated.", "a.c:1:19: warning: unused variable 'u'", "ninja: build stopped: subcommand failed.", "…+"},
			[]string{"int fb(int x){return x+nope;}", "-MF CMakeFiles/b.dir/src/b.c.o.d"}},
		{"build-warn.txt", 0,
			[]string{"[4/4] Linking C executable app", "…+3 build steps"}, []string{"Linking C static library liba.a"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ninja"}, Stdout: strings.NewReader(raw), ExitCode: tc.exit})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller %d >= %d", len(body), len(raw))
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

func TestNoopAndVerboseDecline(t *testing.T) {
	if _, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ninja"}, Stdout: strings.NewReader(fixture(t, "build-noop.txt"))}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("noop: %v", err)
	}
	if _, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ninja", "-v"}, Stdout: strings.NewReader(fixture(t, "build-verbose.txt"))}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("verbose: %v", err)
	}
	if _, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ninja", "-t", "targets"}, Stdout: strings.NewReader("a: phony\n")}); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("tool: %v", err)
	}
}
