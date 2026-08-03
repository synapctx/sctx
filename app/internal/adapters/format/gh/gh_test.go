package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "gh" {
		t.Errorf("Command = %q, want %q", got.Command, "gh")
	}
}

func TestSubcommand(t *testing.T) {
	tests := []struct {
		name   string
		argv   []string
		l1, l2 string
	}{
		{"plain two-level", []string{"gh", "pr", "list"}, "pr", "list"},
		{"global -R with value", []string{"gh", "-R", "owner/repo", "pr", "list"}, "pr", "list"},
		{"global --repo=value", []string{"gh", "--repo=owner/repo", "issue", "list"}, "issue", "list"},
		{"single level", []string{"gh", "auth"}, "auth", ""},
		{"no subcommand", []string{"gh"}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l1, l2, _ := subcommand(tc.argv)
			if l1 != tc.l1 || l2 != tc.l2 {
				t.Errorf("subcommand(%v) = (%q, %q), want (%q, %q)", tc.argv, l1, l2, tc.l1, tc.l2)
			}
		})
	}
}

func TestUnknownSubcommandInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"gh", "auth", "status"},
		Stdout: strings.NewReader("some output\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestJSONBailsOut(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"gh", "pr", "list", "--json", "number,title"},
		Stdout: strings.NewReader(`[{"number":1,"title":"x"}]`),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestNonZeroExitDegradesAggressive(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"gh", "pr", "list"},
		Stdout:   strings.NewReader(""),
		Stderr:   strings.NewReader("error connecting to api.github.com\n"),
		ExitCode: 1,
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}

	in2 := format.Input{
		Argv:     []string{"gh", "pr", "list"},
		Stdout:   strings.NewReader(""),
		Stderr:   strings.NewReader("error connecting to api.github.com\n"),
		ExitCode: 1,
	}
	out, err := f.Relaxed(context.Background(), in2)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "error connecting") {
		t.Errorf("body missing error signal: %q", out.Body)
	}
}
