package gotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const generateRepeatedStdout = "example.com/mod/gen.go:3: running \"stringer\"\n" +
	"example.com/mod/gen.go:3: running \"stringer\"\n" +
	"example.com/mod/gen.go:3: running \"stringer\"\n"

const generateFailStderr = "gen.go:3: running \"stringer\": exit status 1: stringer: type Color not found\n"

func TestAggressive_Generate(t *testing.T) {
	f := New()

	t.Run("quiet successful run is tier-inapplicable", func(t *testing.T) {
		in := newInput([]string{"go", "generate", "./..."}, "go generate", "", "", 0, 0)
		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for empty output", err)
		}
	})

	t.Run("repeated directive announcements dedupe", func(t *testing.T) {
		in := newInput([]string{"go", "generate", "./..."}, "go generate", generateRepeatedStdout, "", 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if strings.Count(body, "running \"stringer\"") != 1 {
			t.Errorf("Body = %q, want the repeated directive deduped to one occurrence", body)
		}
	})

	t.Run("generator failure is preserved verbatim", func(t *testing.T) {
		in := newInput([]string{"go", "generate", "./..."}, "go generate", "", generateFailStderr, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "type Color not found") {
			t.Errorf("Body = %q, want the generator error preserved", body)
		}
	})
}
