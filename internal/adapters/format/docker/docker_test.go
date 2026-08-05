package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	if got := f.Descriptor().Command; got != "docker" {
		t.Errorf("Command = %q, want docker", got)
	}
}

func TestSubcommand(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantSub  string
		wantRest []string
	}{
		{"plain", []string{"docker", "ps"}, "ps", nil},
		{"skip context flag", []string{"docker", "--context", "prod", "ps", "-a"}, "ps", []string{"-a"}},
		{"skip host flag", []string{"docker", "-H", "tcp://x", "images"}, "images", nil},
		{"skip bare debug", []string{"docker", "-D", "ps"}, "ps", nil},
		{"skip log-level flag", []string{"docker", "--log-level", "debug", "ps"}, "ps", nil},
		{"compose nested", []string{"docker", "compose", "ps"}, "compose ps", nil},
		{"compose with global flag", []string{"docker", "--context", "prod", "compose", "logs", "web"}, "compose logs", []string{"web"}},
		{"no subcommand", []string{"docker"}, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub, rest := subcommand(tc.argv)
			if sub != tc.wantSub {
				t.Errorf("sub = %q, want %q", sub, tc.wantSub)
			}
			if strings.Join(rest, ",") != strings.Join(tc.wantRest, ",") {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestAggressiveNonZeroExitDegrades(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"docker", "ps"},
		Stdout:   strings.NewReader(dockerPsFixture),
		ExitCode: 1,
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveUnknownSubcommandInapplicable(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "inspect", "abc"}, Stdout: strings.NewReader("{}")}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
