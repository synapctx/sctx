package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const kubectlGetPodsFixture = `NAME                     READY   STATUS             RESTARTS   AGE
web-7d8f9c6b5d-abcde     1/1     Running            0          3d
web-7d8f9c6b5d-fghij     1/1     Running            0          3d
api-6b8f9c6b5d-klmno     0/1     CrashLoopBackOff   5          10m
db-0                     1/1     Running            0          7d
cache-5f6a7b8c9d-pqrst   1/1     Running            0          1d
worker-abc123            0/1     Pending            0          2m
`

const kubectlGetNoResourcesFixture = "No resources found in default namespace.\n"

func TestAggressiveGet(t *testing.T) {
	f := New()

	t.Run("groups healthy rows and keeps unhealthy rows in full minus AGE", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader(kubectlGetPodsFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "6 items (2 not ready)") {
			t.Errorf("body missing summary, got: %q", body)
		}
		if !strings.Contains(body, "4 ready:") {
			t.Errorf("body missing ready group, got: %q", body)
		}
		for _, want := range []string{
			"web-7d8f9c6b5d-abcde", "db-0", "cache-5f6a7b8c9d-pqrst",
			"api-6b8f9c6b5d-klmno 0/1 CrashLoopBackOff 5",
			"worker-abc123 0/1 Pending 0",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "3d") || strings.Contains(body, "10m") {
			t.Errorf("body still contains AGE column: %q", body)
		}
		if len(out.Body) >= len(kubectlGetPodsFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(kubectlGetPodsFixture))
		}
	})

	t.Run("no resources passes through verbatim", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader(kubectlGetNoResourcesFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if got, want := string(out.Body), strings.TrimRight(kubectlGetNoResourcesFixture, "\n"); got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})

	t.Run("empty stdout is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader("")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("many ready rows cap the listed names", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("NAME       READY   STATUS    RESTARTS   AGE\n")
		for i := 0; i < 12; i++ {
			b.WriteString("pod-" + string(rune('a'+i)) + "         1/1     Running   0          1d\n")
		}
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader(b.String())}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "…+4 more") {
			t.Errorf("body missing capped-list marker, got: %q", body)
		}
	})
}
