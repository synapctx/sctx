package kubectl

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

type execStub struct {
	seen format.Input
}

func (s *execStub) Descriptor() format.Match { return format.Match{Command: "go"} }
func (s *execStub) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	s.seen = in
	body, _ := io.ReadAll(in.Stdout)
	return format.Rendered{Body: append([]byte("delegated:"), body...)}, nil
}
func (s *execStub) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return s.Aggressive(context.Background(), in)
}

func TestExecArgv(t *testing.T) {
	tests := []struct {
		name string
		rest []string
		want []string
		ok   bool
	}{
		{"simple", []string{"pod/x", "--", "go", "test", "./..."}, []string{"go", "test", "./..."}, true},
		{"container flags", []string{"deploy/x", "-c", "app", "--quiet", "--", "/usr/local/go/bin/go", "test"}, []string{"/usr/local/go/bin/go", "test"}, true},
		{"interactive short", []string{"pod/x", "-it", "--", "go", "test"}, nil, false},
		{"interactive long", []string{"pod/x", "--stdin", "--", "go", "test"}, nil, false},
		{"shell", []string{"pod/x", "--", "/bin/sh", "-c", "go test"}, nil, false},
		{"missing separator", []string{"pod/x", "go", "test"}, nil, false},
		{"missing command", []string{"pod/x", "--"}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := execArgv(tc.rest)
			if ok != tc.ok || strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Fatalf("execArgv(%v) = %v, %v; want %v, %v", tc.rest, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestExecDelegatesNativeOutputAndExit(t *testing.T) {
	stub := &execStub{}
	f := New(func(argv []string) (format.Formatter, bool) {
		return stub, len(argv) > 0 && argv[0] == "go"
	})
	in := format.Input{
		Argv:     []string{"kubectl", "--context", "dev", "exec", "pod/x", "-c", "app", "--", "go", "test", "./..."},
		Stdout:   strings.NewReader("native go output\n"),
		Stderr:   strings.NewReader("command terminated with exit code 1\n"),
		ExitCode: 1,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out.Body); got != "delegated:native go output\n" {
		t.Fatalf("body = %q", got)
	}
	if stub.seen.ExitCode != 1 || strings.Join(stub.seen.Argv, " ") != "go test ./..." {
		t.Fatalf("inner input = %#v", stub.seen)
	}
}

func TestExecTransportFailureAndUnknownCommandDecline(t *testing.T) {
	stub := &execStub{}
	resolver := func(argv []string) (format.Formatter, bool) {
		return stub, len(argv) > 0 && argv[0] == "go"
	}
	for _, in := range []format.Input{
		{Argv: []string{"kubectl", "exec", "missing", "--", "go", "test"}, Stderr: strings.NewReader("Error from server (NotFound): pods missing not found\n"), ExitCode: 1},
		{Argv: []string{"kubectl", "exec", "pod/x", "--", "unknown"}, Stdout: strings.NewReader("anything")},
	} {
		if _, err := New(resolver).Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Aggressive(%v) error = %v", in.Argv, err)
		}
	}
}
