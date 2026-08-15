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
		Argv:     []string{"gh", "pr", "checks"},
		Stdout:   strings.NewReader(stdout),
		ExitCode: 1,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "3 checks: 1 fail, 2 pass") {
		t.Errorf("missing summary line: %q", body)
	}
	if !strings.Contains(body, "lint\tfail\t30s\thttps://example/2") {
		t.Errorf("missing failing check: %q", body)
	}
	if !strings.Contains(body, "…+2 passing checks") {
		t.Errorf("missing passing-check elision: %q", body)
	}
}
