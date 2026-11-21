package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/gotest"
	"github.com/synapctx/sctx/internal/domain/format"
)

func goResolver(seen *[]string) Resolver {
	return func(argv []string) (format.Formatter, bool) {
		if seen != nil {
			*seen = append([]string(nil), argv...)
		}
		return gotest.New(), len(argv) > 1 && argv[0] == "go" && argv[1] == "test"
	}
}

// A failing `go test` inside a container: the failing test name and its
// message must survive delegation, and the inner formatter sees the inner argv.
func TestRunDelegatesFailingGoTestWithoutLosingTheError(t *testing.T) {
	raw := dockerNativeFixture(t, "run-go-test-fail.stdout")
	tests := []struct {
		name string
		argv []string
		want string // argv the inner formatter must be resolved with
	}{
		{"docker run mount+workdir", []string{"docker", "run", "--rm", "-v", "/work/proj/mod:/w", "-w", "/w", "golang:1.23-alpine", "go", "test", "./..."}, "go test ./..."},
		{"attached shorthands", []string{"docker", "run", "--rm", "-v/work/proj/mod:/w", "-w/w", "-eGOFLAGS=-count=1", "golang:1.23-alpine", "go", "test", "./..."}, "go test ./..."},
		{"docker global flags", []string{"docker", "--context", "desktop-linux", "run", "--rm", "--platform", "linux/arm64", "golang:1.23-alpine", "go", "test", "./..."}, "go test ./..."},
		{"entrypoint replaces image entrypoint", []string{"docker", "run", "--rm", "--entrypoint", "go", "golang:1.23-alpine", "test", "./..."}, "go test ./..."},
		{"compose run -T", []string{"docker", "compose", "run", "--rm", "-T", "tool", "go", "test", "./..."}, "go test ./..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen []string
			f := New(goResolver(&seen))
			in := format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw), ExitCode: 1}
			for _, tier := range []string{"aggressive", "relaxed"} {
				in.Stdout = strings.NewReader(raw)
				var out format.Rendered
				var err error
				if tier == "aggressive" {
					out, err = f.Aggressive(context.Background(), in)
				} else {
					out, err = f.Relaxed(context.Background(), in)
				}
				if err != nil {
					if tier == "relaxed" && err == format.ErrTierInapplicable {
						continue // the Go formatter's own relaxed tier may decline
					}
					t.Fatalf("%s: %v", tier, err)
				}
				for _, want := range []string{"TestAddBroken", "want 5"} {
					if !strings.Contains(string(out.Body), want) {
						t.Errorf("%s lost %q: %q", tier, want, out.Body)
					}
				}
			}
			if strings.Join(seen, " ") != tt.want {
				t.Fatalf("inner argv = %v, want %q", seen, tt.want)
			}
		})
	}
}

func TestRunDeclines(t *testing.T) {
	ok := dockerNativeFixture(t, "run-go-version.stdout")
	tests := []struct {
		name   string
		argv   []string
		stderr string
		exit   int
	}{
		{"interactive", []string{"docker", "run", "--rm", "-it", "golang:1.23-alpine", "go", "test"}, "", 0},
		{"separate -i", []string{"docker", "run", "-i", "--rm", "golang:1.23-alpine", "go", "test"}, "", 0},
		{"detached", []string{"docker", "run", "-d", "golang:1.23-alpine", "go", "test"}, "", 0},
		{"unknown flag", []string{"docker", "run", "--bogus", "golang:1.23-alpine", "go", "test"}, "", 0},
		{"image default command", []string{"docker", "run", "--rm", "golang:1.23-alpine"}, "", 0},
		{"shell", []string{"docker", "run", "--rm", "golang:1.23-alpine", "sh", "-c", "go test"}, "", 0},
		{"entrypoint with spaces", []string{"docker", "run", "--entrypoint", "go test", "golang:1.23-alpine", "./..."}, "", 0},
		{"flag missing value", []string{"docker", "run", "--rm", "-v"}, "", 0},
		{"compose without -T", []string{"docker", "compose", "run", "--rm", "tool", "go", "test"}, "", 0},
		{"unknown inner program", []string{"docker", "run", "--rm", "golang:1.23-alpine", "frobnicate", "x"}, "", 0},
		{"daemon failure exit 125", []string{"docker", "run", "--rm", "registry.invalid/nope:1", "go", "test"}, dockerNativeFixture(t, "run-noimage.stderr"), 125},
		{"runtime create failed", []string{"docker", "run", "--rm", "golang:1.23-alpine", "go", "test"}, dockerNativeFixture(t, "run-nonexistent-cmd.stderr"), 127},
		{"first-run pull noise", []string{"docker", "run", "--rm", "golang:1.23-alpine", "go", "test"}, "Unable to find image 'golang:1.23-alpine' locally\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(goResolver(nil))
			in := format.Input{Argv: tt.argv, Stdout: strings.NewReader(ok), Stderr: strings.NewReader(tt.stderr), ExitCode: tt.exit}
			if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("Aggressive err = %v, want ErrTierInapplicable", err)
			}
			in.Stdout, in.Stderr = strings.NewReader(ok), strings.NewReader(tt.stderr)
			if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("Relaxed err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestPodmanRunDelegates(t *testing.T) {
	raw := dockerNativeFixture(t, "run-go-test-fail.stdout")
	var seen []string
	p := NewPodman(goResolver(&seen))
	in := format.Input{Argv: []string{"podman", "run", "--rm", "golang:1.23-alpine", "go", "test", "./..."}, Stdout: strings.NewReader(raw), ExitCode: 1}
	if _, err := p.Aggressive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, " ") != "go test ./..." {
		t.Fatalf("inner argv = %v", seen)
	}
}
