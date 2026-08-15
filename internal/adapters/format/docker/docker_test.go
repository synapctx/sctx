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

func TestExplicitOutputContractsBypassBothTiers(t *testing.T) {
	tests := []format.Input{
		{Argv: []string{"docker", "ps", "--format", "json"}, Stdout: strings.NewReader("{\"ID\":\"abc\"}\n")},
		{Argv: []string{"docker", "images", "-q"}, Stdout: strings.NewReader("abc\n")},
		{Argv: []string{"docker", "inspect", "--format={{.State.Status}}", "x"}, Stdout: strings.NewReader("running\n")},
		{Argv: []string{"docker", "build", "--progress=json", "."}, Stdout: strings.NewReader("{\"status\":\"ok\"}\n")},
		{Argv: []string{"docker", "compose", "--progress", "json", "build"}, Stdout: strings.NewReader("{\"status\":\"ok\"}\n")},
	}
	for _, in := range tests {
		if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Aggressive(%v) error = %v", in.Argv, err)
		}
		in.Stdout = strings.NewReader("native\n")
		if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Relaxed(%v) error = %v", in.Argv, err)
		}
	}
}

func TestBinaryAndUnknownCommandsBypassRelaxed(t *testing.T) {
	for _, argv := range [][]string{{"docker", "save", "image"}, {"docker", "export", "container"}, {"docker", "events"}} {
		if _, err := New().Relaxed(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader("bytes")}); err != format.ErrTierInapplicable {
			t.Errorf("Relaxed(%v) error = %v", argv, err)
		}
	}
}

func TestAllStructuredDockerFailuresStayVerbatim(t *testing.T) {
	commands := [][]string{
		{"docker", "ps"}, {"docker", "images"}, {"docker", "logs", "x"},
		{"docker", "build", "."}, {"docker", "pull", "x"}, {"docker", "push", "x"},
		{"docker", "inspect", "x"}, {"docker", "stats", "--no-stream"},
		{"docker", "history", "x"}, {"docker", "top", "x"},
		{"docker", "network", "ls"}, {"docker", "volume", "ls"},
		{"docker", "compose", "ps"}, {"docker", "compose", "up", "-d"},
		{"docker", "compose", "build"}, {"docker", "compose", "logs"},
		{"docker", "compose", "down"},
	}
	for _, argv := range commands {
		in := format.Input{Argv: argv, Stderr: strings.NewReader("native diagnostic\n"), ExitCode: 1}
		if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Aggressive(%v) error = %v", argv, err)
		}
		in.Stderr = strings.NewReader("native diagnostic\n")
		if _, err := New().Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Relaxed(%v) error = %v", argv, err)
		}
	}
}
