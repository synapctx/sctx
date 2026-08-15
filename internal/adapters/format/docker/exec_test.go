package docker

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/gotest"
	"github.com/synapctx/sctx/internal/domain/format"
)

type execStub struct{ seen format.Input }

func (s *execStub) Descriptor() format.Match { return format.Match{Command: "go"} }
func (s *execStub) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	s.seen = in
	body, _ := io.ReadAll(in.Stdout)
	return format.Rendered{Body: append([]byte("delegated:"), body...)}, nil
}

func TestNativeExecDelegatesToRealGoFormatter(t *testing.T) {
	raw := dockerNativeFixture(t, "exec-go-test.stdout")
	direct, directErr := gotest.New().Aggressive(context.Background(), format.Input{Argv: []string{"go", "test", "./..."}, Command: "go test", Stdout: strings.NewReader(raw)})
	if directErr != nil {
		t.Fatalf("direct Go formatter: %v", directErr)
	}
	if !strings.Contains(string(direct.Body), "ok: 18 packages") {
		t.Fatalf("direct Go body = %q", direct.Body)
	}
	var seen []string
	f := New(func(argv []string) (format.Formatter, bool) {
		seen = append([]string(nil), argv...)
		return gotest.New(), len(argv) > 1 && argv[0] == "go" && argv[1] == "test"
	})
	out, err := f.Aggressive(context.Background(), format.Input{
		Argv:   []string{"docker", "exec", "-w", "/fixturego", "container", "go", "test", "./..."},
		Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatalf("%v (resolved argv %v)", err, seen)
	}
	if body := string(out.Body); !strings.Contains(body, "ok: 18 packages") {
		t.Fatalf("body = %q", body)
	}
}
func (s *execStub) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return s.Aggressive(context.Background(), in)
}

func TestExecDelegatesNativeOutputAndExit(t *testing.T) {
	stub := &execStub{}
	f := New(func(argv []string) (format.Formatter, bool) {
		return stub, len(argv) > 0 && argv[0] == "go"
	})
	in := format.Input{
		Argv:   []string{"docker", "exec", "-e", "A=B", "container", "go", "test", "./..."},
		Stdout: strings.NewReader("native go output\n"), ExitCode: 1,
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

func TestComposeExecDelegatesOnlyExplicitNonInteractive(t *testing.T) {
	stub := &execStub{}
	f := New(func(argv []string) (format.Formatter, bool) { return stub, argv[0] == "go" })
	in := format.Input{
		Argv:   []string{"docker", "compose", "-p", "demo", "exec", "-T", "--interactive=false", "api", "go", "test"},
		Stdout: strings.NewReader("ok\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	in.Argv = []string{"docker", "compose", "exec", "api", "go", "test"}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("default interactive compose exec error = %v", err)
	}
}

func TestExecTransportFailureAndUnknownCommandDecline(t *testing.T) {
	stub := &execStub{}
	resolver := func(argv []string) (format.Formatter, bool) { return stub, len(argv) > 0 && argv[0] == "go" }
	inputs := []format.Input{
		{Argv: []string{"docker", "exec", "missing", "go", "test"}, Stderr: strings.NewReader("Error response from daemon: No such container: missing\n"), ExitCode: 1},
		{Argv: []string{"docker", "exec", "container", "unknown"}, Stdout: strings.NewReader("anything")},
	}
	for _, in := range inputs {
		if _, err := New(resolver).Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Aggressive(%v) error = %v", in.Argv, err)
		}
	}
}
