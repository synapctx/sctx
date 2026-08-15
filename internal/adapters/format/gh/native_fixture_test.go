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
		{"search-prs.stdout", []string{"gh", "search", "prs", "--repo", "cli/cli"}},
		{"search-commits.stdout", []string{"gh", "search", "commits", "fix", "--repo", "cli/cli"}},
		{"workflow-list.stdout", []string{"gh", "workflow", "list", "-R", "cli/cli"}},
		{"cache-list.stdout", []string{"gh", "cache", "list", "-R", "cli/cli"}},
		{"gist-list.stdout", []string{"gh", "gist", "list", "--public"}},
	} {
		raw := ghFixture(t, tt.name)
		out, err := f.Aggressive(context.Background(), format.Input{Argv: tt.argv, Stdout: bytes.NewReader(raw)})
		if err != nil || len(out.Body) >= len(raw) {
			t.Errorf("%s render length %d/%d, error %v", tt.name, len(out.Body), len(raw), err)
		}
	}
}

func TestCapturedShortGistFileListStaysVerbatim(t *testing.T) {
	_, err := New().Aggressive(context.Background(), format.Input{
		Argv:   []string{"gh", "gist", "view", "4bb3037b0d5d5b33fb547db4644e24d3", "--files"},
		Stdout: bytes.NewReader(ghFixture(t, "gist-files.stdout")),
	})
	if err != format.ErrTierInapplicable {
		t.Fatalf("short gist file list error = %v, want verbatim fallback", err)
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

func TestCapturedProjectScopeFailureFallsThroughExactly(t *testing.T) {
	for name, call := range map[string]func(context.Context, format.Input) (format.Rendered, error){
		"aggressive": New().Aggressive, "relaxed": New().Relaxed,
	} {
		_, err := call(context.Background(), format.Input{
			Argv:   []string{"gh", "project", "list", "--owner", "cli"},
			Stderr: bytes.NewReader(ghFixture(t, "project-scope.stderr")), ExitCode: 1,
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
