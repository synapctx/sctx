package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptorAndDedicatedCoverage(t *testing.T) {
	f := New()
	if got := f.Descriptor().Command; got != "gh" {
		t.Fatalf("Command = %q, want gh", got)
	}
	for _, argv := range [][]string{
		{"gh", "-R", "cli/cli", "pr", "list"},
		{"gh", "pr", "-Rcli/cli", "checks", "1"},
		{"gh", "api", "repos/cli/cli"},
	} {
		if !f.Dedicated(argv) {
			t.Errorf("Dedicated(%v) = false", argv)
		}
	}
	for _, argv := range [][]string{{"gh", "auth", "status"}, {"gh", "pr", "create"}} {
		if f.Dedicated(argv) {
			t.Errorf("Dedicated(%v) = true", argv)
		}
	}
}

func TestCustomOutputDeclinesBothTiers(t *testing.T) {
	f := New()
	for _, argv := range [][]string{
		{"gh", "pr", "list", "--json", "number", "--jq", ".[0]"},
		{"gh", "issue", "list", "--template={{.}}"},
	} {
		for name, call := range map[string]func(context.Context, format.Input) (format.Rendered, error){
			"aggressive": f.Aggressive, "relaxed": f.Relaxed,
		} {
			_, err := call(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader("custom\n")})
			if err != format.ErrTierInapplicable {
				t.Errorf("%s(%v) error = %v, want ErrTierInapplicable", name, argv, err)
			}
		}
	}
}

func TestRequestedJSONUsesShapeOnlyCompaction(t *testing.T) {
	raw := "[\n  {\n    \"number\": 1,\n    \"title\": \"fix\"\n  }\n]\n"
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "pr", "list", "--json", "number,title"}, Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if got := string(out.Body); strings.Contains(got, "\n") || !strings.Contains(got, `"number":1`) {
		t.Fatalf("JSON was not compacted faithfully: %q", got)
	}
}

func TestFailuresDeclineBothTiers(t *testing.T) {
	f := New()
	for name, call := range map[string]func(context.Context, format.Input) (format.Rendered, error){
		"aggressive": f.Aggressive, "relaxed": f.Relaxed,
	} {
		_, err := call(context.Background(), format.Input{
			Argv: []string{"gh", "pr", "list"}, Stderr: strings.NewReader("error connecting to api.github.com\n"), ExitCode: 1,
		})
		if err != format.ErrTierInapplicable {
			t.Errorf("%s error = %v, want ErrTierInapplicable", name, err)
		}
	}
}

func TestRelaxedUnknownShapeOnlyCollapsesExactRuns(t *testing.T) {
	out, err := New().Relaxed(context.Background(), format.Input{
		Argv: []string{"gh", "repo", "view"}, Stdout: strings.NewReader("progress\nprogress\nprogress\ndone\n"),
	})
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if got := string(out.Body); !strings.Contains(got, "progress ×3") || !strings.Contains(got, "done") {
		t.Fatalf("unexpected relaxed output: %q", got)
	}
}

func TestRelaxedBinaryDeclines(t *testing.T) {
	_, err := New().Relaxed(context.Background(), format.Input{
		Argv: []string{"gh", "api", "endpoint"}, Stdout: strings.NewReader("a\x00b"),
	})
	if err != format.ErrTierInapplicable {
		t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}
