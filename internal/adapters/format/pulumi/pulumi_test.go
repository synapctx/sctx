package pulumi

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	if e, err := os.ReadFile("testdata/" + name + ".exit"); err == nil && strings.TrimSpace(string(e)) != "0" {
		exit = 1
	}
	return string(b), exit
}

func in(argv []string, stdout, stderr string, exit int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "pulumi" {
		t.Fatal("descriptor")
	}
}

// Fixtures are REAL pulumi v3.277.0 output (file backend, YAML runtime, random
// provider; macOS arm64), hostnames scrubbed.
func TestRealFixtures(t *testing.T) {
	cases := []struct {
		name     string
		sub      string
		must     []string
		mustNot  []string
		declines bool
	}{
		{name: "up", sub: "up",
			must:    []string{"Updating (dev):", "random:index:RandomPet pet created", "pulumi:pulumi:Stack demo-dev created", `petName: "bursting-parrot"`, "+ 4 created", "Duration: 1s"},
			mustNot: []string{"Previewing update", "4 to create", " creating (0s)", "Downloading plugin"}},
		{name: "up-change", sub: "up",
			must:    []string{"created replacement", "replaced", "deleted original", `~ petName: "bursting-parrot" => "honestly-glorious-haddock"`, "+-2 replaced"},
			mustNot: []string{"Previewing update", "(0s)"}},
		{name: "destroy", sub: "destroy",
			must:    []string{"Destroying (dev):", "random:index:RandomPet pet deleted", "- 4 deleted", "pulumi stack rm dev"},
			mustNot: []string{"Previewing destroy", " deleting (0s)"}},
		// Error-only runs have nothing droppable: the tier steps aside and the
		// diagnostics reach the caller byte for byte.
		{name: "up-fail", sub: "up", declines: true},
		{name: "preview-fail", sub: "preview", declines: true},
		{name: "preview", sub: "preview", declines: true},
		{name: "stack", sub: "stack", declines: true},
		{name: "preview-nochange", sub: "preview", declines: true},
		{name: "preview-change", sub: "preview", declines: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, exit := load(t, tc.name)
			r, err := New().Aggressive(context.Background(), in([]string{"pulumi", tc.sub}, raw, "", exit))
			if tc.declines {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("want decline, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.must {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, w := range tc.mustNot {
				if strings.Contains(body, w) {
					t.Errorf("kept %q", w)
				}
			}
			if len(body) >= len(raw) || !r.Elided || !strings.HasPrefix(body, "…+") {
				t.Errorf("size %d/%d elided=%v marker=%q", len(body), len(raw), r.Elided, body[:min(20, len(body))])
			}
			golden := filepath.Join("testdata", "golden", tc.name+".golden")
			if *update {
				_ = os.MkdirAll(filepath.Dir(golden), 0o755)
				if err := os.WriteFile(golden, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil || string(want) != body {
				t.Errorf("golden mismatch (run -update): %v", err)
			}
		})
	}
}

// A failure after the preview ran keeps both the error and the update section.
func TestFailureAfterPreviewKeepsError(t *testing.T) {
	raw := "Previewing update (dev):\n\n +  a:b:C x create \n\nResources:\n    + 1 to create\n\nUpdating (dev):\n\n +  a:b:C x creating (0s) \n +  a:b:C x **creating failed** error: boom\nDiagnostics:\n  a:b:C (x):\n    error: boom\n\nResources:\n    1 failed\n"
	r, err := New().Aggressive(context.Background(), in([]string{"pulumi", "up", "--yes"}, raw, "", 1))
	if err != nil {
		t.Fatal(err)
	}
	b := string(r.Body)
	if strings.Count(b, "boom") != 2 || !strings.Contains(b, "1 failed") || strings.Contains(b, "to create") {
		t.Fatalf("got:\n%s", b)
	}
}

func TestDeclines(t *testing.T) {
	raw, _ := load(t, "up")
	for _, argv := range [][]string{
		{"pulumi", "stack", "ls"}, {"pulumi", "up", "--json"}, {"pulumi", "preview", "--diff"}, {"pulumi", "watch"}, {"pulumi"},
	} {
		if _, err := New().Aggressive(context.Background(), in(argv, raw, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}

func TestRelaxedKeepsPreview(t *testing.T) {
	raw, _ := load(t, "up")
	r, err := New().Relaxed(context.Background(), in([]string{"pulumi", "up"}, raw, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "Previewing update") || strings.Contains(string(r.Body), "creating (0s)") {
		t.Fatalf("relaxed:\n%s", r.Body)
	}
}
