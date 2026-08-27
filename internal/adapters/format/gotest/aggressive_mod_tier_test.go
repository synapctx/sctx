package gotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures captured from real `go mod tidy`/`go mod graph` runs against
// scratch modules.

const modTidyNoiseStderr = "go: downloading example.com/foo v1.2.3\n" +
	"go: downloading example.com/bar v0.1.0\n" +
	"go: finding module for package example.com/baz\n" +
	"go: extracting example.com/foo v1.2.3\n" +
	"go: downloading example.com/qux v2.0.0\n"

const modTidyErrorStderr = "go: updates to go.mod needed, disabled by -mod=readonly\n" +
	"\tto update it: go mod tidy\n"

const modVerifyErrorStdout = "example.com/foo v1.2.3: checksum mismatch\n" +
	"\tdownloaded: h1:abc...\n" +
	"\tgo.sum:     h1:def...\n"

func modGraphFixture(edges int) string {
	var b strings.Builder
	for range edges {
		b.WriteString("example.com/root example.com/dep")
		b.WriteByte('\n')
	}
	return b.String()
}

func TestAggressive_Mod(t *testing.T) {
	f := New()

	t.Run("quiet successful tidy has nothing to compress", func(t *testing.T) {
		in := newInput([]string{"go", "mod", "tidy"}, "go mod tidy", "", "", 0, 0)
		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for empty output", err)
		}
	})

	t.Run("download progress noise collapses to a count, errors kept", func(t *testing.T) {
		in := newInput([]string{"go", "mod", "tidy"}, "go mod tidy", "", modTidyNoiseStderr, 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "…+5 modules") {
			t.Errorf("Body = %q, want a collapsed module-fetch marker", body)
		}
		for _, noisy := range []string{"go: downloading", "go: finding", "go: extracting"} {
			if strings.Contains(body, noisy) {
				t.Errorf("Body = %q, should not contain raw progress noise %q", body, noisy)
			}
		}
	})

	t.Run("go.mod update diagnostic is preserved verbatim", func(t *testing.T) {
		in := newInput([]string{"go", "mod", "tidy"}, "go mod tidy", "", modTidyErrorStderr, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "go: updates to go.mod needed") {
			t.Errorf("Body = %q, want the go.mod diagnostic preserved", body)
		}
		if !rendered.FoldStderr {
			t.Error("FoldStderr = false, want true when stderr carried a diagnostic")
		}
	})

	t.Run("checksum mismatch from go mod verify is preserved verbatim", func(t *testing.T) {
		in := newInput([]string{"go", "mod", "verify"}, "go mod verify", modVerifyErrorStdout, "", 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "checksum mismatch") {
			t.Errorf("Body = %q, want the checksum diagnostic preserved", body)
		}
	})

	t.Run("go mod graph caps a long edge list with a marker", func(t *testing.T) {
		stdout := modGraphFixture(maxModGraphEdges + 12)
		in := newInput([]string{"go", "mod", "graph"}, "go mod graph", stdout, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "…+12 more edges") {
			t.Errorf("Body = %q, want a capped-edges marker", body)
		}
		if strings.Count(body, "example.com/root example.com/dep") != maxModGraphEdges {
			t.Errorf("Body kept %d edges, want exactly %d", strings.Count(body, "example.com/root example.com/dep"), maxModGraphEdges)
		}
	})
}
