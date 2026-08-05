package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const dockerImagesFixture = `REPOSITORY   TAG       IMAGE ID       CREATED         SIZE
nginx        1.25      1a2b3c4d5e6f   3 weeks ago     187MB
postgres     16        2b3c4d5e6f1a   2 months ago    438MB
<none>       <none>    3c4d5e6f1a2b   4 months ago    120MB
<none>       <none>    4d5e6f1a2b3c   5 months ago    95MB
`

func TestAggressiveImages(t *testing.T) {
	f := New()

	t.Run("renders repo:tag size and collapses dangling", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "images"}, Stdout: strings.NewReader(dockerImagesFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "4 images, total ≈") {
			t.Errorf("body missing summary, got: %q", body)
		}
		for _, want := range []string{"nginx:1.25 187MB", "postgres:16 438MB", "+2 dangling"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "1a2b3c4d5e6f") {
			t.Errorf("body still contains image ID: %q", body)
		}
		if len(out.Body) >= len(dockerImagesFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(dockerImagesFixture))
		}
	})

	t.Run("no images", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "images"}, Stdout: strings.NewReader("")}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if got := string(out.Body); got != "0 images" {
			t.Errorf("body = %q, want %q", got, "0 images")
		}
	})
}

func TestParseSizeFormatSize(t *testing.T) {
	v, ok := parseSize("187MB")
	if !ok || v != 187e6 {
		t.Errorf("parseSize(187MB) = %v, %v", v, ok)
	}
	if got := formatSize(625e6); got != "625MB" {
		t.Errorf("formatSize(625e6) = %q, want 625MB", got)
	}
}
