package grep

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestPathContentMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var sb strings.Builder
	for i := 0; i < 12; i++ {
		sb.WriteString("a.go:\tfunc one(ctx context.Context) error\n")
	}
	sb.WriteString("a.go:\tfunc two(ctx context.Context)\nb.go:var ctx context.Context\n")
	raw := sb.String()

	t.Run("rg without -n groups by file and collapses repeats", func(t *testing.T) {
		out, err := New().Aggressive(context.Background(), input(raw, []string{"rg", "context.Context"}))
		if err != nil {
			t.Fatalf("Aggressive: %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{"a.go (13)", "func one(ctx context.Context) error ×12", "func two(ctx context.Context)", "b.go (1)", "var ctx context.Context"} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in %q", want, body)
			}
		}
		if len(body) >= len(raw) {
			t.Errorf("not smaller: %d >= %d", len(body), len(raw))
		}
	})

	t.Run("a path token that is not a real file declines", func(t *testing.T) {
		in := "image.tag: v1\nimage.name: foo\n"
		if _, err := New().Aggressive(context.Background(), input(in, []string{"grep", "image", "values.yaml"})); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("count output is never regrouped", func(t *testing.T) {
		if _, err := New().Aggressive(context.Background(), input("a.go:3\nb.go:1\n", []string{"grep", "-rc", "x"})); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestContextFlagsDecline(t *testing.T) {
	raw := "a.go:10:match\na.go-11-context\n--\na.go:20:match\na.go-21-context\n"
	for _, argv := range [][]string{
		{"grep", "-rn", "-A1", "match"}, {"rg", "-C2", "match"}, {"rg", "--after-context=2", "m"}, {"grep", "-nB", "3", "m"}, {"grep", "-2", "m"},
	} {
		if _, err := New().Aggressive(context.Background(), input(raw, argv)); err != format.ErrTierInapplicable {
			t.Errorf("%v: err = %v, want ErrTierInapplicable (context lines must not be dropped)", argv, err)
		}
	}
}
