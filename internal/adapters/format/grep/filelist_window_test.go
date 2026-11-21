package grep

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestFileListFolding(t *testing.T) {
	f := New()
	cases := []struct {
		name    string
		argv    []string
		raw     string
		want    []string
		wantErr bool
	}{
		{
			name: "grep -rl folds shared directories",
			argv: []string{"grep", "-rl", "TODO", "."},
			raw:  "./internal/adapters/a/x.go\n./internal/adapters/a/y.go\n./internal/adapters/a/w.go\n./internal/adapters/b/z.go\n./cmd/main.go\n./README.md\n",
			want: []string{"6 files in 4 dirs", "./internal/adapters/a/: x.go, y.go, w.go", "./internal/adapters/b/: z.go", "./cmd/: main.go", "./: README.md"},
		},
		{
			name: "rg --files-with-matches",
			argv: []string{"rg", "--files-with-matches", "foo"},
			raw:  "pkg/long/dir/name/one.go\npkg/long/dir/name/two.go\npkg/long/dir/name/three.go\npkg/long/dir/name/four.go\n",
			want: []string{"4 files in 1 dirs", "pkg/long/dir/name/: one.go, two.go, three.go, four.go"},
		},
		{
			name:    "no -l flag stays inapplicable",
			argv:    []string{"grep", "foo"},
			raw:     "pkg/a/a.go\npkg/b/b.go\npkg/c/c.go\npkg/d/d.go\n",
			wantErr: true,
		},
		{
			name:    "too short a list stays verbatim",
			argv:    []string{"grep", "-l", "foo"},
			raw:     "a/a.go\nb/b.go\n",
			wantErr: true,
		},
		{
			name:    "one file per dir does not shrink",
			argv:    []string{"grep", "-l", "foo"},
			raw:     "a/a\nb/b\nc/c\nd/d\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := f.Aggressive(context.Background(), input(tc.raw, tc.argv))
			if tc.wantErr {
				if err != format.ErrTierInapplicable {
					t.Fatalf("err = %v, want ErrTierInapplicable", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in %q", w, body)
				}
			}
			if len(body) >= len(tc.raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(tc.raw))
			}
		})
	}
}

func TestFileListRealCapture(t *testing.T) {
	rawBytes, err := os.ReadFile("testdata/grep-l.txt")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawBytes)
	out, err := New().Aggressive(context.Background(), input(raw, []string{"grep", "-rl", "func", "."}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	t.Logf("raw=%d body=%d", len(raw), len(body))
	// every listed file name must survive
	for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
		name := l[strings.LastIndex(l, "/")+1:]
		if !strings.Contains(body, name) {
			t.Fatalf("lost %s", l)
		}
	}
	if len(body) >= len(raw) {
		t.Errorf("not smaller")
	}
}

func TestPatternRegexp(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string // regexp source, "" for nil
	}{
		{"positional", []string{"grep", "-rn", "needle", "."}, "needle"},
		{"-e", []string{"grep", "-e", "needle", "x"}, "needle"},
		{"value flag skipped", []string{"rg", "-A", "3", "needle"}, "needle"},
		{"-F quotes", []string{"grep", "-F", "a.b"}, `a\.b`},
		{"-i folds", []string{"grep", "-i", "abc"}, "(?i)abc"},
		{"two -e is unknown", []string{"grep", "-e", "a", "-e", "b"}, ""},
		{"-f is unknown", []string{"grep", "-f", "pats", "x"}, ""},
		{"BRE that RE2 rejects", []string{"grep", `a(`}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re := patternRegexp(tc.argv)
			switch {
			case tc.want == "" && re != nil:
				t.Errorf("got %q want nil", re)
			case tc.want != "" && (re == nil || re.String() != tc.want):
				t.Errorf("got %v want %q", re, tc.want)
			}
		})
	}
}

func TestTruncateAround(t *testing.T) {
	long := strings.Repeat("a", 300) + "NEEDLE" + strings.Repeat("b", 300)
	re := patternRegexp([]string{"grep", "NEEDLE"})
	cases := []struct {
		name       string
		in         string
		re         bool
		wantHas    string
		wantPrefix string
		wantSuffix string
	}{
		{"match beyond head is centred", long, true, "NEEDLE", "…", "…"},
		{"no pattern keeps head window", long, false, "", "aaaa", "…"},
		{"match inside head keeps head window", "xx NEEDLE " + strings.Repeat("c", 300), true, "NEEDLE", "xx", "…"},
		{"short line untouched", "short NEEDLE", true, "short NEEDLE", "short", "NEEDLE"},
		{"match at very end is not cut", strings.Repeat("a", 400) + "NEEDLE", true, "NEEDLE", "…", "NEEDLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r = re
			if !tc.re {
				r = nil
			}
			got := truncateAround(tc.in, maxContentLen, r)
			if n := len([]rune(got)); n > maxContentLen+2 {
				t.Errorf("window %d runes, want <= %d", n, maxContentLen+2)
			}
			if !strings.Contains(got, tc.wantHas) {
				t.Errorf("missing %q in %q", tc.wantHas, got)
			}
			if !strings.HasPrefix(got, tc.wantPrefix) || !strings.HasSuffix(got, tc.wantSuffix) {
				t.Errorf("edges wrong: %q", got)
			}
		})
	}
}

func TestAggressiveCentresLongLineOnPattern(t *testing.T) {
	line := strings.Repeat("x", 300) + "NEEDLE" + strings.Repeat("y", 50)
	raw := "f.go:7:" + line + "\n"
	out, err := New().Aggressive(context.Background(), input(raw, []string{"grep", "-n", "NEEDLE", "f.go"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "NEEDLE") {
		t.Fatalf("match scrolled out of the window: %q", out.Body)
	}
}
