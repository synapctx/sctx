package taskrunner

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/gotest"
	"github.com/synapctx/sctx/internal/domain/format"
)

func resolver(argv []string) (format.Formatter, bool) {
	if len(argv) >= 2 && argv[0] == "go" {
		return gotest.New(), true
	}
	return nil, false
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exitOf(t *testing.T, name string) int {
	n, err := strconv.Atoi(strings.TrimSpace(read(t, name+".exit")))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func find(t *testing.T, command string, sub ...string) format.Formatter {
	t.Helper()
	for _, f := range All(resolver) {
		m := f.Descriptor()
		if m.Command == command && strings.Join(m.Subcommands, " ") == strings.Join(sub, " ") {
			return f
		}
	}
	t.Fatalf("no formatter for %s %v", command, sub)
	return nil
}

func input(t *testing.T, name string, argv ...string) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(read(t, name+".stdout")), Stderr: strings.NewReader(read(t, name+".stderr")), ExitCode: exitOf(t, name)}
}

func TestDelegatesToInnerFormatter(t *testing.T) {
	cases := []struct {
		name string
		f    format.Formatter
		argv []string
		keep []string
	}{
		{"just-test", find(t, "just"), []string{"just", "test"}, []string{"go test -v ./...", "TestBad", "got 1 want 2", "error: recipe `test` failed on line 2 with exit code 1"}},
		{"task-test", find(t, "task"), []string{"task", "test"}, []string{"task: [test] go test -v ./...", "TestBad", "task: Failed to run task \"test\": exit status 1"}},
		{"mise-test", find(t, "mise", "run"), []string{"mise", "run", "test"}, []string{"[test] $ go test -v ./...", "TestBad", "[test] ERROR task failed"}},
	}
	for _, tc := range cases {
		for tier, fn := range map[string]func(context.Context, format.Input) (format.Rendered, error){"aggressive": tc.f.Aggressive, "relaxed": tc.f.Relaxed} {
			t.Run(tc.name+"/"+tier, func(t *testing.T) {
				in := input(t, tc.name, tc.argv...)
				raw := read(t, tc.name+".stdout") + read(t, tc.name+".stderr")
				out, err := fn(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				body := string(out.Body)
				if len(body) >= len(raw) {
					t.Errorf("not smaller: %d >= %d", len(body), len(raw))
				}
				if !out.FoldStderr {
					t.Error("stderr must be folded: banner and footer live there")
				}
				for _, k := range tc.keep {
					if !strings.Contains(body, k) {
						t.Errorf("lost %q:\n%s", k, body)
					}
				}
			})
		}
	}
}

func TestCompileErrorSurvivesDelegation(t *testing.T) {
	for _, name := range []string{"just-build-fail", "task-build-fail", "mise-build-fail"} {
		t.Run(name, func(t *testing.T) {
			var f format.Formatter
			var argv []string
			switch {
			case strings.HasPrefix(name, "just"):
				f, argv = find(t, "just"), []string{"just", "build"}
			case strings.HasPrefix(name, "task"):
				f, argv = find(t, "task"), []string{"task", "build"}
			default:
				f, argv = find(t, "mise", "run"), []string{"mise", "run", "build"}
			}
			out, err := f.Aggressive(context.Background(), input(t, name, argv...))
			if err != nil {
				// A tiny failure may not shrink; declining keeps every byte.
				if errors.Is(err, format.ErrTierInapplicable) {
					return
				}
				t.Fatal(err)
			}
			if !strings.Contains(string(out.Body), `./bad.go:2:23: cannot use "x"`) {
				t.Errorf("compile error lost:\n%s", out.Body)
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	ok := input(t, "just-test", "just", "test")
	for name, tc := range map[string]struct {
		f    format.Formatter
		argv []string
		out  string
		err  string
	}{
		"list flag":      {find(t, "just"), []string{"just", "--list"}, "test\n", "go test\n"},
		"dry run":        {find(t, "just"), []string{"just", "-n", "test"}, "", "go test\n"},
		"task watch":     {find(t, "task"), []string{"task", "-w", "test"}, "x\n", "task: [test] go test ./...\n"},
		"unknown inner":  {find(t, "task"), []string{"task", "x"}, "x\n", "task: [x] echo hi\n"},
		"no banner":      {find(t, "task"), []string{"task", "x"}, "x\n", "task: Task \"x\" is up to date\n"},
		"two commands":   {find(t, "task"), []string{"task", "x"}, "x\n", "task: [a] go build ./...\ntask: [b] go test ./...\n"},
		"just plain cmd": {find(t, "just"), []string{"just", "x"}, "hi\n", "echo hi\n"},
		"piped recipe":   {find(t, "task"), []string{"task", "x"}, "x\n", "task: [t] go test ./... > out.txt\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.f.Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(tc.out), Stderr: strings.NewReader(tc.err)})
			if !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("got %v", err)
			}
		})
	}
	_ = ok
}

func TestNilResolverDeclines(t *testing.T) {
	f := All(nil)[1]
	_, err := f.Aggressive(context.Background(), input(t, "task-test", "task", "test"))
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("got %v", err)
	}
}
