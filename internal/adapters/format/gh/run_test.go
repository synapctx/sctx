package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveRunListWithFailures(t *testing.T) {
	f := New()
	stdout := strings.Join([]string{
		"completed\tsuccess\tCI\tmain\tpush\t1001\t2m3s\t5m ago",
		"completed\tfailure\tCI\tfeature-x\tpush\t1002\t1m1s\t10m ago",
		"in_progress\t\tCI\tmain\tpush\t1003\t30s\tjust now",
	}, "\n")
	in := format.Input{
		Argv:   []string{"gh", "run", "list"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "3 runs (1 failed)") {
		t.Errorf("missing summary line: %q", body)
	}
	if !strings.Contains(body, "completed failure CI feature-x 10m ago") {
		t.Errorf("missing failed row: %q", body)
	}
}
