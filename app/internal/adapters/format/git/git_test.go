package git

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "git" {
		t.Errorf("Command = %q, want %q", got.Command, "git")
	}
}

func TestSubcommand(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"plain", []string{"git", "status"}, "status"},
		{"global -C flag", []string{"git", "-C", "/repo", "status"}, "status"},
		{"global -c flag", []string{"git", "-c", "color.ui=false", "log"}, "log"},
		{"combined flags", []string{"git", "--no-pager", "-C", "/repo", "diff"}, "diff"},
		{"no subcommand", []string{"git"}, ""},
		{"only flags", []string{"git", "--version"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := subcommand(tc.argv)
			if got != tc.want {
				t.Errorf("subcommand(%v) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestUnknownSubcommandInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"git", "blame"},
		Stdout: strings.NewReader("some blame output\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestNonZeroExitDegradesReadOnlyAggressive(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"git", "log"},
		Stdout:   strings.NewReader(""),
		Stderr:   strings.NewReader("fatal: bad revision 'nope'\n"),
		ExitCode: 128,
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
	// Relaxed tier must still preserve the fatal error.
	in2 := format.Input{
		Argv:     []string{"git", "log"},
		Stdout:   strings.NewReader(""),
		Stderr:   strings.NewReader("fatal: bad revision 'nope'\n"),
		ExitCode: 128,
	}
	out, err := f.Relaxed(context.Background(), in2)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "fatal: bad revision") {
		t.Errorf("body missing fatal error: %q", out.Body)
	}
}
