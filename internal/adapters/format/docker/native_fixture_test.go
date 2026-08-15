package docker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func dockerNativeFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertNativeShrinks(t *testing.T, argv []string, stream, raw string, wants ...string) {
	t.Helper()
	in := format.Input{Argv: argv}
	if stream == "stderr" {
		in.Stderr = strings.NewReader(raw)
	} else {
		in.Stdout = strings.NewReader(raw)
	}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%v body missing %q: %q", argv, want, body)
		}
	}
	if len(out.Body) >= len(raw) {
		t.Fatalf("%v native fixture did not shrink: %d >= %d", argv, len(out.Body), len(raw))
	}
}

func TestNativeDocker29Tables(t *testing.T) {
	assertNativeShrinks(t, []string{"docker", "ps", "-a"}, "stdout", dockerNativeFixture(t, "ps-a.stdout"),
		"6 containers", "up·unhealthy", "exit(7)")
	assertNativeShrinks(t, []string{"docker", "images"}, "stdout", dockerNativeFixture(t, "images-v29.stdout"),
		"2 images", "intentionally-very-long-repository")
	assertNativeShrinks(t, []string{"docker", "stats", "--no-stream"}, "stdout", dockerNativeFixture(t, "stats.stdout"),
		"3 containers", "cpu=", "mem=")
	assertNativeShrinks(t, []string{"docker", "history", "sctx-formatter-test:20260815"}, "stdout", dockerNativeFixture(t, "history.stdout"), "14 layers")
	assertNativeShrinks(t, []string{"docker", "top", "fixture"}, "stdout", dockerNativeFixture(t, "top.stdout"), "1 processes", "sleep 3600")
	assertNativeShrinks(t, []string{"docker", "network", "ls"}, "stdout", dockerNativeFixture(t, "network-ls.stdout"), "1 networks")
	assertNativeShrinks(t, []string{"docker", "volume", "ls"}, "stdout", dockerNativeFixture(t, "volume-ls.stdout"), "1 volumes")
}

func TestNativeDockerLogsKeepUniqueDiagnostics(t *testing.T) {
	raw := dockerNativeFixture(t, "logs.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"docker", "compose", "logs"}, Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"compose-repeat ×30", "COMPOSE_UNIQUE_SENTINEL", "COMPOSE_FAILED_SENTINEL", "COMPOSE_RESTART_SENTINEL ×4"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}
}

func TestNativeBuildKitSuccessAndFailure(t *testing.T) {
	assertNativeShrinks(t, []string{"docker", "build", "--progress=plain", "."}, "stderr", dockerNativeFixture(t, "build-success.stderr"),
		"build steps", "RUN echo \"BUILD_STEP_OUTPUT_SENTINEL\"", "naming to docker.io/library/sctx-formatter-test:20260815")

	raw := dockerNativeFixture(t, "build-failure.stderr")
	in := format.Input{Argv: []string{"docker", "build", "."}, Stderr: strings.NewReader(raw), ExitCode: 1}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("failure aggressive error = %v", err)
	}
	in.Stderr = strings.NewReader(raw)
	_, err := New().Relaxed(context.Background(), in)
	if err != format.ErrTierInapplicable {
		t.Fatalf("failure relaxed error = %v, want verbatim", err)
	}
	for _, want := range []string{"FAILING_LAYER_SENTINEL", "Dockerfile.fail:3", "exit code: 17", "github.com/docker/buildx/commands.runBuild"} {
		if !strings.Contains(raw, want) {
			t.Errorf("native failure fixture missing %q", want)
		}
	}
}

func TestNativeComposeTablesAndLifecycle(t *testing.T) {
	assertNativeShrinks(t, []string{"docker", "compose", "ps", "-a"}, "stdout", dockerNativeFixture(t, "compose-ps.stdout"),
		"3 containers", "failed exit(7)", "healthy up·healthy")
	assertNativeShrinks(t, []string{"docker", "compose", "up", "-d"}, "stderr", dockerNativeFixture(t, "compose-up.stderr"),
		"4 resources", "Network sctx-formatter-20260815_default: created", "Container sctx-formatter-20260815-healthy-1: started")
	assertNativeShrinks(t, []string{"docker", "compose", "down"}, "stderr", dockerNativeFixture(t, "compose-down.stderr"),
		"4 resources", "Network sctx-formatter-20260815_default: removed")
}

func TestNativePullAndInspect(t *testing.T) {
	assertNativeShrinks(t, []string{"docker", "pull", "golang:1.26-alpine"}, "stdout", dockerNativeFixture(t, "pull-success.stdout"),
		"Pulling from library/golang", "…+6 layers", "Digest:", "Status:")
	assertNativeShrinks(t, []string{"docker", "inspect", "fixture"}, "stdout", dockerNativeFixture(t, "inspect.stdout"),
		`"Status":"healthy"`, `"com.synapctx.sctx.formatter-test":"true"`)
}

func TestNativeExecTransportFailureDeclines(t *testing.T) {
	raw := dockerNativeFixture(t, "exec-transport-error.stderr")
	f := New(func([]string) (format.Formatter, bool) { return &execStub{}, true })
	in := format.Input{Argv: []string{"docker", "exec", "missing", "go", "test"}, Stderr: strings.NewReader(raw), ExitCode: 1}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("transport failure error = %v", err)
	}
}
