package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestRelaxedFilter(t *testing.T) {
	f := New()

	t.Run("preserves structure and marks exact repeats", func(t *testing.T) {
		raw := "header\n\n----\nrepeated\nrepeated\nrepeated\nfooter\n"
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader(raw)}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "\n\n----\n") {
			t.Errorf("body lost blank/separator structure: %q", body)
		}
		if !strings.Contains(body, "repeated ×3") {
			t.Errorf("body missing dedupe marker, got: %q", body)
		}
	})

	t.Run("unknown and structured output decline", func(t *testing.T) {
		for _, in := range []format.Input{
			{Argv: []string{"kubectl", "completion", "zsh"}, Stdout: strings.NewReader("line\nline\nline\n")},
			{Argv: []string{"kubectl", "apply", "-f", "x", "-o", "yaml"}, Stdout: strings.NewReader("apiVersion: v1\nkind: Pod\n")},
		} {
			if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("Relaxed(%v) error = %v", in.Argv, err)
			}
		}
	})

	t.Run("empty input is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods"}, Stdout: strings.NewReader("")}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
