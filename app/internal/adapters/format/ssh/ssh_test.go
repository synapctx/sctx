package ssh

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// fake records what it was asked to render, so the tests can assert on the DELEGATION
// rather than on some downstream formatter's output.
type fake struct {
	gotArgv    []string
	gotCommand string
	gotExit    int
	calls      int
}

func (f *fake) Descriptor() format.Match { return format.Match{Command: "fake"} }
func (f *fake) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	f.calls++
	f.gotArgv, f.gotCommand, f.gotExit = in.Argv, in.Command, in.ExitCode
	return format.Rendered{Body: []byte("rendered by inner\n")}, nil
}
func (f *fake) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.Aggressive(ctx, in)
}

func newWith(t *testing.T, inner *fake, resolvable bool) format.Formatter {
	t.Helper()
	return New(func(argv []string) (format.Formatter, bool) {
		if !resolvable {
			return nil, false
		}
		return inner, true
	})
}

func in(argv []string, exit int) format.Input {
	return format.Input{
		Argv:     argv,
		Command:  "ssh",
		Stdout:   strings.NewReader("CONTAINER ID   IMAGE   STATUS\nabc123  nginx  Up 2 days\n"),
		Stderr:   strings.NewReader(""),
		ExitCode: exit,
	}
}

func TestDelegatesToTheRemoteCommandsFormatter(t *testing.T) {
	for name, tc := range map[string]struct {
		argv     []string
		wantArgv []string
		wantCmd  string
	}{
		// The shell strips the quotes, so this arrives as a single argv element.
		"quoted remote command":  {[]string{"ssh", "vm", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
		"unquoted remote":        {[]string{"ssh", "vm", "docker", "ps"}, []string{"docker", "ps"}, "docker ps"},
		"user@host":              {[]string{"ssh", "deploy@198.51.100.7", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
		"valueless flag":         {[]string{"ssh", "-q", "vm", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
		"clustered flags":        {[]string{"ssh", "-qt", "vm", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
		"flag with separate val": {[]string{"ssh", "-p", "2222", "vm", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
		"flag with attached val": {[]string{"ssh", "-p2222", "vm", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
		"-o option":              {[]string{"ssh", "-o", "StrictHostKeyChecking=no", "vm", "go test ./..."}, []string{"go", "test", "./..."}, "go test"},
		"-i identity":            {[]string{"ssh", "-i", "/k/id_ed25519", "vm", "kubectl get pods"}, []string{"kubectl", "get", "pods"}, "kubectl get"},
		"after --":               {[]string{"ssh", "--", "vm", "docker ps"}, []string{"docker", "ps"}, "docker ps"},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &fake{}
			f := newWith(t, inner, true)
			out, err := f.Aggressive(context.Background(), in(tc.argv, 0))
			if err != nil {
				t.Fatalf("Aggressive() error = %v, want delegation", err)
			}
			if inner.calls != 1 {
				t.Fatalf("inner formatter called %d times, want 1", inner.calls)
			}
			if strings.Join(inner.gotArgv, " ") != strings.Join(tc.wantArgv, " ") {
				t.Errorf("inner argv = %v, want %v", inner.gotArgv, tc.wantArgv)
			}
			if inner.gotCommand != tc.wantCmd {
				t.Errorf("inner Command = %q, want %q", inner.gotCommand, tc.wantCmd)
			}
			if string(out.Body) != "rendered by inner\n" {
				t.Errorf("body = %q, want the inner formatter's render", out.Body)
			}
		})
	}
}

func TestDeclinesWhenThereIsNothingSafeToDo(t *testing.T) {
	for name, tc := range map[string]struct {
		argv []string
		exit int
		why  string
	}{
		"interactive session": {[]string{"ssh", "vm"}, 0,
			"no remote command: the output is an interactive shell's"},
		"options only": {[]string{"ssh", "-q"}, 0,
			"no host at all"},
		"bare ssh": {[]string{"ssh"}, 0,
			"nothing to inspect"},
		"-N no command": {[]string{"ssh", "-N", "vm"}, 0,
			"-N explicitly runs no remote command"},
		"-T no tty": {[]string{"ssh", "-T", "vm", "docker ps"}, 0,
			"-T is used for tunnels and non-command sessions"},
		"ssh's own failure": {[]string{"ssh", "vm", "docker ps"}, 255,
			"255 is ssh's own failure: the output is ssh's, not docker's"},
		"compound: &&": {[]string{"ssh", "vm", "cd /opt && docker ps"}, 0,
			"two programs produced this output"},
		"compound: pipe": {[]string{"ssh", "vm", "docker ps | head -3"}, 0,
			"a downstream filter already transformed it"},
		"compound: semicolon": {[]string{"ssh", "vm", "docker ps; ls"}, 0,
			"two programs"},
		"compound: redirect": {[]string{"ssh", "vm", "docker ps > out.txt"}, 0,
			"output went to a file, so what we have is not it"},
		"compound: substitution": {[]string{"ssh", "vm", "docker ps $(hostname)"}, 0,
			"unknown expansion"},
		"nested ssh": {[]string{"ssh", "a", "ssh b docker ps"}, 0,
			"would recurse through this same formatter"},
		"already wrapped": {[]string{"ssh", "vm", "sctx docker ps"}, 0,
			"double wrapping is meaningless"},
		"long option": {[]string{"ssh", "--weird", "vm", "docker ps"}, 0,
			"ssh has no long options; stop guessing"},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &fake{}
			f := newWith(t, inner, true)
			_, err := f.Aggressive(context.Background(), in(tc.argv, tc.exit))
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("Aggressive() error = %v, want ErrTierInapplicable — %s", err, tc.why)
			}
			if inner.calls != 0 {
				t.Errorf("inner formatter was called %d times; it must not see output it cannot safely parse (%s)", inner.calls, tc.why)
			}
		})
	}
}

func TestDeclinesWhenNoFormatterExistsForTheRemoteCommand(t *testing.T) {
	inner := &fake{}
	f := newWith(t, inner, false) // resolver finds nothing
	if _, err := f.Aggressive(context.Background(), in([]string{"ssh", "vm", "somethingexotic --flag"}, 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("error = %v, want ErrTierInapplicable when the remote program has no formatter", err)
	}
	if inner.calls != 0 {
		t.Error("inner formatter called despite the resolver reporting no match")
	}
}

func TestNilResolverIsInert(t *testing.T) {
	// A delegating formatter with nothing to delegate to must decline, not panic.
	f := New(nil)
	if _, err := f.Aggressive(context.Background(), in([]string{"ssh", "vm", "docker ps"}, 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("Aggressive() error = %v, want ErrTierInapplicable", err)
	}
	if _, err := f.Relaxed(context.Background(), in([]string{"ssh", "vm", "docker ps"}, 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("Relaxed() error = %v, want ErrTierInapplicable", err)
	}
}

func TestRemoteExitCodeReachesTheInnerFormatter(t *testing.T) {
	// A formatter decides what to retain based on whether the command failed. ssh returns
	// the REMOTE command's exit code, so passing it through is what keeps the inner
	// formatter's error handling correct — dropping it would make a failed remote build
	// render as a successful one.
	inner := &fake{}
	f := newWith(t, inner, true)
	if _, err := f.Aggressive(context.Background(), in([]string{"ssh", "vm", "go test ./..."}, 1)); err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if inner.gotExit != 1 {
		t.Errorf("inner ExitCode = %d, want 1", inner.gotExit)
	}
}

func TestRelaxedDelegatesToo(t *testing.T) {
	inner := &fake{}
	f := newWith(t, inner, true)
	if _, err := f.Relaxed(context.Background(), in([]string{"ssh", "vm", "docker ps"}, 0)); err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("inner called %d times from Relaxed, want 1", inner.calls)
	}
}

func TestRemoteArgvHostParsing(t *testing.T) {
	// The option table is load-bearing: reading -p as valueless makes "2222" the host and
	// shifts the remote command by one element, so the wrong formatter gets chosen.
	got, ok := remoteArgv([]string{"ssh", "-p", "2222", "-o", "BatchMode=yes", "root@vm", "docker", "ps", "-a"})
	if !ok {
		t.Fatal("remoteArgv() declined a well-formed invocation")
	}
	if want := "docker ps -a"; strings.Join(got, " ") != want {
		t.Errorf("remoteArgv() = %q, want %q", strings.Join(got, " "), want)
	}
}
