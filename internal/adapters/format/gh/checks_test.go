package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveChecks(t *testing.T) {
	f := New()
	stdout := strings.Join([]string{
		"build\tpass\t1m2s\thttps://example/1",
		"lint\tfail\t30s\thttps://example/2",
		"unit-tests\tpass\t2m0s\thttps://example/3",
	}, "\n")
	in := format.Input{
		Argv:   []string{"gh", "pr", "checks"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "3 checks (1 failing)") {
		t.Errorf("missing summary line: %q", body)
	}
	if !strings.Contains(body, "lint fail") {
		t.Errorf("missing failing check: %q", body)
	}
}
