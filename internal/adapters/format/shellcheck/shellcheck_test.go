package shellcheck

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

func in(t *testing.T, name string, argv ...string) format.Input {
	t.Helper()
	out, err := os.ReadFile(filepath.Join("testdata", name+".stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errb, _ := os.ReadFile(filepath.Join("testdata", name+".stderr"))
	return format.Input{
		Argv:     append([]string{"shellcheck"}, argv...),
		Stdout:   strings.NewReader(string(out)),
		Stderr:   strings.NewReader(string(errb)),
		ExitCode: 1,
	}
}

func TestAggressiveGolden(t *testing.T) {
	got, err := New().Aggressive(context.Background(), in(t, "default", "bad.sh", "bad2.sh"))
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "default.golden")
	if *update {
		if err := os.WriteFile(golden, got.Body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != string(want) {
		t.Fatalf("golden mismatch:\n%s", got.Body)
	}
	raw, _ := os.ReadFile(filepath.Join("testdata", "default.stdout"))
	if len(got.Body) >= len(raw) {
		t.Fatalf("not smaller: %d >= %d", len(got.Body), len(raw))
	}
	if !got.Elided {
		t.Error("fix suggestions dropped, Elided must be set")
	}
}

// Every finding's code and line:col must survive, per the raw report.
func TestNoFindingLost(t *testing.T) {
	got, err := New().Aggressive(context.Background(), in(t, "default", "bad.sh", "bad2.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(got.Body)
	raw, _ := os.ReadFile(filepath.Join("testdata", "default.stdout"))
	if n, m := strings.Count(string(raw), " (info): ")+strings.Count(string(raw), " (warning): ")+
		strings.Count(string(raw), " (style): ")+strings.Count(string(raw), " (error): "),
		strings.Count(body, "\n  "); n != m {
		t.Fatalf("raw findings %d, rendered %d", n, m)
	}
	for _, want := range []string{"SC2086=", "2:12 info SC2035", "bad2.sh (5)", "dropped:"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
}

func TestDeclines(t *testing.T) {
	tests := []struct {
		name string
		in   format.Input
	}{
		{"json format", in(t, "default", "-f", "json", "bad.sh")},
		{"format=", in(t, "default", "--format=gcc", "bad.sh")},
		{"stderr present", in(t, "missing", "nonexist.sh")},
		{"empty", format.Input{Argv: []string{"shellcheck", "x"}, Stdout: strings.NewReader("")}},
		{"unknown shape", format.Input{Argv: []string{"shellcheck", "x"}, Stdout: strings.NewReader("hello\nworld\n")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New().Aggressive(context.Background(), tc.in); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
}

func TestRelaxedMarksWikiLinks(t *testing.T) {
	got, err := New().Relaxed(context.Background(), in(t, "default", "bad.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Body), "shellcheck.net") || !strings.Contains(string(got.Body), "…+3 wiki links") {
		t.Fatalf("unexpected relaxed body:\n%s", got.Body)
	}
}
