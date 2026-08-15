package gh

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func ghFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCapturedNativeFixtures(t *testing.T) {
	f := New()
	for _, tt := range []struct {
		name string
		argv []string
	}{
		{"pr-list.stdout", []string{"gh", "pr", "list", "-R", "cli/cli"}},
		{"run-list.stdout", []string{"gh", "run", "list", "-R", "cli/cli"}},
		{"pr-checks.stdout", []string{"gh", "pr", "checks", "14148", "-R", "cli/cli"}},
	} {
		raw := ghFixture(t, tt.name)
		out, err := f.Aggressive(context.Background(), format.Input{Argv: tt.argv, Stdout: bytes.NewReader(raw)})
		if err != nil || len(out.Body) >= len(raw) {
			t.Errorf("%s render length %d/%d, error %v", tt.name, len(out.Body), len(raw), err)
		}
	}
}

func TestCapturedFailureFallsThroughExactly(t *testing.T) {
	f := New()
	for name, call := range map[string]func(context.Context, format.Input) (format.Rendered, error){
		"aggressive": f.Aggressive, "relaxed": f.Relaxed,
	} {
		_, err := call(context.Background(), format.Input{
			Argv:   []string{"gh", "pr", "view", "999999999"},
			Stderr: bytes.NewReader(ghFixture(t, "not-found.stderr")), ExitCode: 1,
		})
		if err != format.ErrTierInapplicable {
			t.Errorf("%s error = %v, want ErrTierInapplicable", name, err)
		}
	}
}

func TestAPIPaginationContracts(t *testing.T) {
	f := New()
	adjacent := ghFixture(t, "api-pages.stdout")
	if _, err := f.Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "api", "--paginate", "endpoint"}, Stdout: bytes.NewReader(adjacent),
	}); err != format.ErrTierInapplicable {
		t.Fatalf("adjacent pages error = %v, want authoritative fallback", err)
	}
	slurp := ghFixture(t, "api-slurp.stdout")
	out, err := f.Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "api", "--paginate", "--slurp", "endpoint"}, Stdout: bytes.NewReader(slurp),
	})
	if err != nil || len(out.Body) >= len(slurp) {
		t.Fatalf("slurped JSON render length %d/%d, error %v", len(out.Body), len(slurp), err)
	}
}
