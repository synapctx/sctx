package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	if got := f.Descriptor().Command; got != "kubectl" {
		t.Errorf("Command = %q, want kubectl", got)
	}
}

func TestSubcommand(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantSub  string
		wantRest []string
	}{
		{"plain", []string{"kubectl", "get", "pods"}, "get", []string{"pods"}},
		{"skip namespace flag", []string{"kubectl", "-n", "prod", "get", "pods"}, "get", []string{"pods"}},
		{"skip namespace long flag", []string{"kubectl", "--namespace", "prod", "get", "pods"}, "get", []string{"pods"}},
		{"skip context flag", []string{"kubectl", "--context", "dev", "describe", "pod", "web"}, "describe", []string{"pod", "web"}},
		{"all value globals", []string{"kubectl", "--as", "alice", "--cluster", "dev", "--request-timeout", "5s", "-s", "https://cluster", "get", "pods"}, "get", []string{"pods"}},
		{"boolean globals", []string{"kubectl", "--warnings-as-errors", "--disable-compression=false", "get", "pods"}, "get", []string{"pods"}},
		{"equals form does not consume", []string{"kubectl", "-n=prod", "get", "pods"}, "get", []string{"pods"}},
		{"attached shorthand", []string{"kubectl", "-nprod", "-v5", "get", "pods"}, "get", []string{"pods"}},
		{"unknown global declines", []string{"kubectl", "--unknown", "value", "get", "pods"}, "", nil},
		{"no subcommand", []string{"kubectl"}, "", nil},
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

func TestOutputFormat(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"get", "pods", "-o", "json"}, "json"},
		{[]string{"get", "pods", "--output=yaml"}, "yaml"},
		{[]string{"get", "pods", "-o=wide"}, "wide"},
		{[]string{"pod/x", "--", "tool", "-o", "json"}, ""},
		{[]string{"get", "pods"}, ""},
	}
	for _, tc := range tests {
		if got := outputFormat(tc.args); got != tc.want {
			t.Errorf("outputFormat(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestAggressiveNonZeroExitDegrades(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"kubectl", "get", "pods"},
		Stdout:   strings.NewReader(kubectlGetPodsFixture),
		ExitCode: 1,
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveGetJSONBailsOut(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "get", "pods", "-o", "json"}, Stdout: strings.NewReader(`{"items":[]}`)}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveUnknownSubcommandInapplicable(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "explain", "pod.spec"}, Stdout: strings.NewReader("KIND: Pod\nFIELD: spec")}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
