package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveLsFiles(t *testing.T) {
	f := New()

	t.Run("groups by directory and caps names per dir", func(t *testing.T) {
		var lines []string
		for i := range 20 {
			lines = append(lines, fmt.Sprintf("internal/pkg/file%02d.go", i))
		}
		lines = append(lines, "main.go", "go.mod")
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "ls-files"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "internal/pkg/") {
			t.Errorf("body missing directory grouping: %q", body)
		}
		if !strings.Contains(body, "…+10") {
			t.Errorf("body missing per-dir elision marker: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})
}

func TestAggressiveLsTree(t *testing.T) {
	sha := strings.Repeat("a1", 20)
	var lines []string
	for i := range 14 {
		lines = append(lines, fmt.Sprintf("100644 blob %s\tinternal/pkg/file%02d.go", sha, i))
	}
	lines = append(lines,
		fmt.Sprintf("100755 blob %s\tscripts/run.sh", sha),
		fmt.Sprintf("120000 blob %s\tscripts/link", sha),
		fmt.Sprintf("160000 commit %s\tthird_party/lib", sha),
		fmt.Sprintf("100644 blob %s\tgo.mod", sha))
	raw := strings.Join(lines, "\n") + "\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"git", "ls-tree", "-r", "HEAD"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{"internal/pkg/ (14)", "run.sh*", "link@", "lib!", "go.mod", "object ids and mode omitted"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, sha) {
		t.Errorf("object ids leaked: %q", body)
	}

	t.Run("tree entries and long format decline", func(t *testing.T) {
		tree := fmt.Sprintf("040000 tree %s\tsrc\n", sha)
		if _, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"git", "ls-tree", "HEAD"}, Stdout: strings.NewReader(tree)}); err != format.ErrTierInapplicable {
			t.Errorf("tree entries: err = %v, want ErrTierInapplicable", err)
		}
		if _, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"git", "ls-tree", "-l", "HEAD"}, Stdout: strings.NewReader(raw)}); err != format.ErrTierInapplicable {
			t.Errorf("-l: err = %v, want ErrTierInapplicable", err)
		}
	})
}
