package gotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func listModulesFixture(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("example.com/mod")
		b.WriteByte('\n')
	}
	return b.String()
}

const listJSONFixture = `{"Path":"example.com/mod","Version":"v1.2.3","Dir":"/go/pkg/mod/example.com/mod@v1.2.3","GoMod":"/go/pkg/mod/cache/download/example.com/mod/@v/v1.2.3.mod","GoVersion":"1.22"}
`

func TestAggressive_List(t *testing.T) {
	f := New()

	t.Run("plain module list over the cap gets a marker", func(t *testing.T) {
		stdout := listModulesFixture(maxListEntries + 7)
		in := newInput([]string{"go", "list", "-m", "all"}, "go list", stdout, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "…+7 more") {
			t.Errorf("Body = %q, want a capped-list marker", body)
		}
		if strings.Count(body, "example.com/mod") != maxListEntries {
			t.Errorf("Body kept %d entries, want exactly %d", strings.Count(body, "example.com/mod"), maxListEntries)
		}
	})

	t.Run("short list under the cap is tier-inapplicable", func(t *testing.T) {
		in := newInput([]string{"go", "list", "./..."}, "go list", "example.com/mod/pkg\n", "", 0, 0)

		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable when nothing is saved", err)
		}
	})

	t.Run("go list -json delegates to jsoncompact", func(t *testing.T) {
		in := newInput([]string{"go", "list", "-m", "-json", "all"}, "go list", listJSONFixture, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "example.com/mod") {
			t.Errorf("Body = %q, want the module path preserved", body)
		}
		if strings.Contains(body, "\n  ") {
			t.Errorf("Body = %q, want compact JSON (no indentation)", body)
		}
	})

	t.Run("error signal preserved on failure", func(t *testing.T) {
		in := newInput([]string{"go", "list", "./..."}, "go list", "",
			"go: module lookup error: not found\n", 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(rendered.Body), "module lookup error") {
			t.Errorf("Body = %q, want the error preserved", string(rendered.Body))
		}
	})
}
