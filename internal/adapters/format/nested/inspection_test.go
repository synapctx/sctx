package nested_test

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/docker"
	"github.com/synapctx/sctx/internal/adapters/format/kubectl"
	"github.com/synapctx/sctx/internal/adapters/format/ssh"
	"github.com/synapctx/sctx/internal/domain/format"
)

type inspectionFormatter struct {
	calls int
	seen  format.Input
}

func (f *inspectionFormatter) Descriptor() format.Match { return format.Match{Command: "go"} }
func (f *inspectionFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	f.calls++
	f.seen = in
	return format.Rendered{Body: []byte("inner render")}, nil
}
func (f *inspectionFormatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.Aggressive(ctx, in)
}

// TestTransportDiagnosticsNeverReachInnerFormatters is the shared inspection
// boundary for every nested transport. A transport failure must remain native;
// handing it to (for example) the Go-test formatter could turn "pod missing"
// into a plausible but false test result.
func TestTransportDiagnosticsNeverReachInnerFormatters(t *testing.T) {
	tests := []struct {
		name string
		new  func(resolve func([]string) (format.Formatter, bool)) format.Formatter
		in   format.Input
	}{
		{
			"ssh authentication", func(r func([]string) (format.Formatter, bool)) format.Formatter { return ssh.New(r) },
			format.Input{Argv: []string{"ssh", "host", "go test ./..."}, Stderr: strings.NewReader("Permission denied (publickey).\n"), ExitCode: 255},
		},
		{
			"kubectl missing pod", func(r func([]string) (format.Formatter, bool)) format.Formatter { return kubectl.New(r) },
			format.Input{Argv: []string{"kubectl", "exec", "missing", "--", "go", "test", "./..."}, Stderr: strings.NewReader("Error from server (NotFound): pods missing not found\n"), ExitCode: 1},
		},
		{
			"docker missing container", func(r func([]string) (format.Formatter, bool)) format.Formatter { return docker.New(r) },
			format.Input{Argv: []string{"docker", "exec", "missing", "go", "test", "./..."}, Stderr: strings.NewReader("Error response from daemon: No such container: missing\n"), ExitCode: 1},
		},
		{
			"compose missing service", func(r func([]string) (format.Formatter, bool)) format.Formatter { return docker.New(r) },
			format.Input{Argv: []string{"docker", "compose", "exec", "-T", "--interactive=false", "missing", "go", "test"}, Stderr: strings.NewReader("no such service: missing\n"), ExitCode: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &inspectionFormatter{}
			resolved := 0
			outer := tt.new(func([]string) (format.Formatter, bool) {
				resolved++
				return inner, true
			})
			_, err := outer.Aggressive(context.Background(), tt.in)
			if err != format.ErrTierInapplicable {
				t.Fatalf("Aggressive() error = %v, want native fallback", err)
			}
			if resolved != 0 || inner.calls != 0 {
				t.Fatalf("transport failure reached delegation: resolved=%d calls=%d", resolved, inner.calls)
			}
		})
	}
}

func TestInnerFailuresStillReachInnerFormatter(t *testing.T) {
	tests := []struct {
		name string
		fm   func(func([]string) (format.Formatter, bool)) format.Formatter
		in   format.Input
	}{
		{"ssh", func(r func([]string) (format.Formatter, bool)) format.Formatter { return ssh.New(r) }, format.Input{Argv: []string{"ssh", "host", "go test ./..."}, Stderr: strings.NewReader("package failed\n"), ExitCode: 1}},
		{"kubectl", func(r func([]string) (format.Formatter, bool)) format.Formatter { return kubectl.New(r) }, format.Input{Argv: []string{"kubectl", "exec", "pod/x", "--", "go", "test", "./..."}, Stderr: strings.NewReader("command terminated with exit code 1\n"), ExitCode: 1}},
		{"docker", func(r func([]string) (format.Formatter, bool)) format.Formatter { return docker.New(r) }, format.Input{Argv: []string{"docker", "exec", "container", "go", "test", "./..."}, Stderr: strings.NewReader("package failed\n"), ExitCode: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &inspectionFormatter{}
			outer := tt.fm(func([]string) (format.Formatter, bool) { return inner, true })
			if _, err := outer.Aggressive(context.Background(), tt.in); err != nil {
				t.Fatalf("Aggressive() error = %v", err)
			}
			if inner.calls != 1 || inner.seen.ExitCode != 1 {
				t.Fatalf("inner calls=%d exit=%d", inner.calls, inner.seen.ExitCode)
			}
		})
	}
}
