package kubectl

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func nativeFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNativeGetPodsFixture(t *testing.T) {
	raw := nativeFixture(t, "get-pods.txt")
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{
		"13 items (2 not ready)", "11 ready:", "…+3 more",
		"bad-image 0/1 ImagePullBackOff 0", "crash-loop 0/1 Error 2 (20s ago)",
		"not ready: NAME | READY | STATUS | RESTARTS",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}
	if len(out.Body) >= len(raw) {
		t.Fatalf("native fixture did not shrink: %d >= %d", len(out.Body), len(raw))
	}
}

func TestNativeMultiTableStaysVerbatim(t *testing.T) {
	raw := nativeFixture(t, "get-multiple-types.txt")
	in := format.Input{Argv: []string{"kubectl", "get", "deployments,services"}, Stdout: strings.NewReader(raw)}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v", err)
	}
}

func TestNativeLogsPreserveUniqueSentinel(t *testing.T) {
	raw := nativeFixture(t, "logs-repeated.txt")
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"kubectl", "logs", "pod/x"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(out.Body); !strings.Contains(body, "steady-line ×40") || !strings.Contains(body, "UNIQUE_ERROR_SENTINEL") {
		t.Fatalf("unsafe native log render: %q", body)
	}
}

func TestNativeWarningOnlyEventsAlreadyMinimal(t *testing.T) {
	raw := nativeFixture(t, "events-warning.txt")
	in := format.Input{Argv: []string{"kubectl", "events", "--types=Warning"}, Stdout: strings.NewReader(raw)}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v, want native warning order unchanged", err)
	}
}
