package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestUserSelectedOutputContracts(t *testing.T) {
	f := New()

	t.Run("native JSON compacts for get and mutations", func(t *testing.T) {
		for _, argv := range [][]string{
			{"kubectl", "get", "pods", "-o", "json"},
			{"kubectl", "apply", "-f", "pod.yaml", "--output=json"},
		} {
			raw := "{\n  \"kind\": \"Pod\",\n  \"metadata\": {\"name\": \"x\"}\n}\n"
			out, err := f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw)})
			if err != nil {
				t.Fatalf("Aggressive(%v): %v", argv, err)
			}
			if got := string(out.Body); got != `{"kind":"Pod","metadata":{"name":"x"}}` {
				t.Errorf("body = %q", got)
			}
		}
	})

	t.Run("semantic formats bypass both tiers", func(t *testing.T) {
		for _, formatName := range []string{"yaml", "name", "jsonpath={.metadata.name}", "custom-columns=NAME:.metadata.name", "go-template={{.metadata.name}}"} {
			argv := []string{"kubectl", "get", "pods", "-o", formatName}
			for tier, call := range map[string]func(context.Context, format.Input) (format.Rendered, error){
				"aggressive": f.Aggressive,
				"relaxed":    f.Relaxed,
			} {
				if _, err := call(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader("value\nvalue\nvalue\n")}); err != format.ErrTierInapplicable {
					t.Errorf("%s(%s) error = %v", tier, formatName, err)
				}
			}
		}
	})

	t.Run("exec inner output flag is not kubectl output", func(t *testing.T) {
		if got := outputFormat([]string{"pod/x", "--", "tool", "-o", "json"}); got != "" {
			t.Fatalf("outputFormat = %q", got)
		}
	})
}
