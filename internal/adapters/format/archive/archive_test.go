package archive

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

func run(cmd string, argv []string, raw string, exit int) (format.Rendered, error) {
	f := &Formatter{command: cmd}
	return f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: exit})
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		cmd  string
		argv []string
		keep []string
		drop []string
	}{
		{"tar-tv-gnu.txt", "tar", []string{"tar", "-tvf", "x.tar"},
			[]string{"proj/brew/ ×", "proj/gh/testdata/ ×", ", ", " B", "…+"}, []string{"drwxr-xr-x", "proj/brew/aggressive.go"}},
		{"tar-tv-bsd.txt", "tar", []string{"tar", "-tvf", "x.tar"},
			[]string{"proj/brew/ ×", "…+"}, []string{"drwxr-xr-x", "proj/brew/aggressive.go"}},
		{"tar-c-gnu.txt", "tar", []string{"tar", "-cvf", "x.tar", "proj"},
			[]string{"proj/brew/ ×", "…+"}, []string{"proj/brew/aggressive.go"}},
		{"tar-c-bsd.txt", "tar", []string{"tar", "-cvf", "x.tar", "proj"},
			[]string{"proj/brew/ ×"}, []string{"a proj/brew/aggressive.go"}},
		{"zip-r.txt", "zip", []string{"zip", "-r", "x.zip", "proj"},
			[]string{"proj/brew/ ×", "…+"}, []string{"adding:"}},
		{"unzip-l.txt", "unzip", []string{"unzip", "-l", "x.zip"},
			[]string{"Archive:", "Length      Date    Time    Name", "proj/brew/ ×", "files"}, []string{"proj/brew/aggressive.go"}},
		{"unzip-v.txt", "unzip", []string{"unzip", "-v", "x.zip"},
			[]string{"Archive:", "proj/brew/ ×"}, []string{"proj/brew/aggressive.go"}},
		{"unzip-x.txt", "unzip", []string{"unzip", "x.zip", "-d", "ex"},
			[]string{"Archive:", "ex2/proj/brew/ ×"}, []string{"inflating:"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(tc.cmd, tc.argv, raw, 0)
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

// Every member is accounted for: per-directory counts plus directory entries
// add up to the member lines in the raw listing.
func TestCountsAddUp(t *testing.T) {
	raw := fixture(t, "tar-c-gnu.txt")
	out, err := run("tar", []string{"tar", "-cvf", "x.tar", "proj"}, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, l := range strings.Split(string(out.Body), "\n") {
		var n int
		if i := strings.Index(l, " ×"); i >= 0 {
			n = atoi(strings.TrimSpace(l[i+len(" ×"):]))
		} else if strings.HasPrefix(l, "…+") {
			n = atoi(strings.Fields(strings.TrimPrefix(l, "…+"))[0])
		}
		total += n
	}
	if want := strings.Count(raw, "\n"); total != want {
		t.Errorf("accounted %d of %d members", total, want)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func TestFailuresAndSmallRunsDecline(t *testing.T) {
	cases := []struct {
		file, cmd string
		argv      []string
	}{
		{"tar-c-fail.txt", "tar", []string{"tar", "-cvf", "y.tar", "nonexistdir", "proj/ping"}},
		{"tar-tv-fail.txt", "tar", []string{"tar", "-tvf", "nonexist.tar"}},
		{"unzip-fail.txt", "unzip", []string{"unzip", "-l", "nonexist.zip"}},
		{"zip-fail.txt", "zip", []string{"zip", "-r", "n.zip", "nonexistdir"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			if _, err := run(tc.cmd, tc.argv, fixture(t, tc.file), 1); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestPayloadOnStdoutDeclines(t *testing.T) {
	raw := fixture(t, "tar-tv-gnu.txt")
	for _, c := range []struct {
		cmd  string
		argv []string
	}{
		{"tar", []string{"tar", "-cf", "-", "proj"}},
		{"tar", []string{"tar", "-xOf", "x.tar", "-O"}},
		{"unzip", []string{"unzip", "-p", "x.zip", "f"}},
		{"unzip", []string{"unzip", "-c", "x.zip", "f"}},
		{"zip", []string{"zip", "-q", "-r", "x.zip", "p"}},
	} {
		if _, err := run(c.cmd, c.argv, raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", c.argv, err)
		}
	}
}

func TestNonZeroExitKeepsErrorLines(t *testing.T) {
	raw := fixture(t, "tar-c-gnu.txt") + "tar: proj/secret: Cannot open: Permission denied\ntar: Exiting with failure status due to previous errors\n"
	out, err := run("tar", []string{"tar", "-cvf", "x.tar", "proj"}, raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tar: proj/secret: Cannot open: Permission denied", "tar: Exiting with failure status due to previous errors"} {
		if !strings.Contains(string(out.Body), k) {
			t.Errorf("lost %q", k)
		}
	}
}

func head(raw string, n int) string {
	lines := strings.SplitAfter(raw, "\n")
	return strings.Join(lines[:n], "")
}

// A small listing is a question about names: every name and size survives,
// only the permission/owner/date columns go.
func TestSmallListingKeepsNames(t *testing.T) {
	cases := []struct {
		file string
		cmd  string
		argv []string
	}{
		{"tar-tv-gnu.txt", "tar", []string{"tar", "-tvf", "x.tar"}},
		{"tar-tv-bsd.txt", "tar", []string{"tar", "tvf", "x.tar"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := head(fixture(t, tc.file), 40)
			out, err := run(tc.cmd, tc.argv, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
				f := strings.Fields(l)
				name := f[len(f)-1]
				if !strings.Contains(body, name+" ") {
					t.Errorf("lost member %q", name)
				}
			}
			if strings.Contains(body, "drwxr-xr-x") || !strings.Contains(body, "omitted") {
				t.Errorf("columns not dropped or not marked:\n%s", body)
			}
		})
	}
}

func TestSmallBareListingDeclines(t *testing.T) {
	raw := head(fixture(t, "tar-c-gnu.txt"), 40)
	if _, err := run("tar", []string{"tar", "-tf", "x.tar"}, raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("err = %v", err)
	}
	if _, err := run("tar", []string{"tar", "-cvf", "x.tar", "d"}, raw, 0); err != nil {
		t.Errorf("a 40-line create log must group: %v", err)
	}
}

func TestTarList(t *testing.T) {
	for argv, want := range map[string]bool{
		"tar -tf x": true, "tar -tvf x": true, "tar tvf x": true, "tar -tzf x": true, "tar --list -f x": true,
		"tar -cvf x d": false, "tar -xvf x": false, "tar xzf x": false, "tar -czf x t": false,
	} {
		if got := tarList(strings.Fields(argv)); got != want {
			t.Errorf("tarList(%q) = %v, want %v", argv, got, want)
		}
	}
}
