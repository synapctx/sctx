package gotest

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Fixtures in testdata/ are real `go` 1.27.4 stderr captures (macOS arm64).
func stderrFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAggressive_InstallAndModDownloadTrace(t *testing.T) {
	f := New()
	cases := []struct {
		name    string
		argv    []string
		file    string
		exit    int
		want    []string
		smaller bool
	}{
		{"remote install collapses fetch progress", []string{"go", "install", "golang.org/x/tools/cmd/stringer@latest"}, "install_remote.stderr.txt", 0,
			[]string{"…+3 modules fetched"}, true},
		{"build failure keeps every diagnostic", []string{"go", "install", "./buildpkg"}, "install_buildfail.stderr.txt", 1,
			[]string{"# example.com/ts/buildpkg", `buildpkg/b.go:3:23: cannot use "x" (untyped string constant) as int value in return statement`}, false},
		{"unresolvable path keeps the cause", []string{"go", "install", "example.invalid/nothing@latest"}, "install_notfound.stderr.txt", 1,
			[]string{"unrecognized import path", "no such host"}, false},
		{"get upgrade still reports the version change", []string{"go", "get", "golang.org/x/text@v0.3.8"}, "get_upgrade.stderr.txt", 0,
			[]string{"go: upgraded golang.org/x/text v0.3.0 => v0.3.8"}, false},
		{"mod download -x drops 200 traces", []string{"go", "mod", "download", "-x"}, "mod_download_x.stderr.txt", 0,
			[]string{"…+"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := stderrFixture(t, tc.file)
			in := newInput(tc.argv, "go "+tc.argv[1], "", raw, tc.exit, 0)
			r, err := f.Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in %q", w, body)
				}
			}
			if tc.smaller && len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
			}
			if strings.Contains(body, "# get https://proxy.golang.org/golang.org/x/text/@v/v0.3.0.info") {
				t.Errorf("trace kept: %q", body)
			}
		})
	}
}

func TestModDownloadTraceKeepsNon200(t *testing.T) {
	raw := "# get https://proxy.golang.org/x/@v/list\n# get https://proxy.golang.org/x/@v/list: 404 Not Found (0.1s)\ngo: x: reading https://proxy.golang.org/x/@v/list: 404 Not Found\n"
	in := newInput([]string{"go", "mod", "download", "-x"}, "go mod", "", raw, 1, 0)
	r, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if !strings.Contains(body, "404 Not Found (0.1s)") || !strings.Contains(body, "go: x: reading") {
		t.Errorf("failure evidence lost: %q", body)
	}
	if strings.Contains(body, "# get https://proxy.golang.org/x/@v/list\n") {
		t.Errorf("request line kept: %q", body)
	}
}
