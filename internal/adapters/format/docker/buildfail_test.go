package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// A failing BuildKit build must keep the failing step, its output and the
// error excerpt, while finished steps collapse to one line each.
func TestFailedBuildKeepsErrorSignal(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		fix  string
		must []string
	}{
		{"docker build", []string{"docker", "build", "bad"}, "build-failure-buildx.stderr", []string{
			"#6 [3/3] RUN false", "did not complete successfully: exit code: 1", "#5 0.156 step1", "3 | >>> RUN false", "ERROR: failed to build",
		}},
		{"buildx build", []string{"docker", "buildx", "build", "-t", "x", "."}, "build-failure-buildx.stderr", []string{"ERROR: failed to build"}},
		{"compose build", []string{"docker", "compose", "build"}, "build-failure-buildx.stderr", []string{"ERROR: failed to build"}},
		{"existing real failure", []string{"docker", "build", "-f", "Dockerfile.fail", "."}, "build-failure.stderr", []string{"ERROR"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := dockerNativeFixture(t, tc.fix)
			out, err := New().Aggressive(context.Background(), format.Input{
				Argv: tc.argv, Stdout: strings.NewReader(""), Stderr: strings.NewReader(raw), ExitCode: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range tc.must {
				if !strings.Contains(string(out.Body), m) {
					t.Errorf("lost %q in\n%s", m, out.Body)
				}
			}
			if len(out.Body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(out.Body), len(raw))
			}
			if !out.FoldStderr {
				t.Error("stderr was read; FoldStderr must be set")
			}
		})
	}
}

func TestFailedBuildMachineProgressDeclines(t *testing.T) {
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"docker", "build", "--progress=rawjson", "."}, Stderr: strings.NewReader(dockerNativeFixture(t, "build-failure.stderr")), ExitCode: 1,
	})
	if err != format.ErrTierInapplicable {
		t.Fatalf("got %v", err)
	}
}
