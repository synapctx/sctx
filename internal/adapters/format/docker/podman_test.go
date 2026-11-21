package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures in testdata/podman-* were captured from podman 5.x on macOS arm64.
func TestPodmanRealOutput(t *testing.T) {
	p := NewPodman()
	if p.Descriptor().Command != "podman" {
		t.Fatal("descriptor must claim podman")
	}
	tests := []struct {
		name   string
		argv   []string
		fix    string
		stream string
		wants  []string
	}{
		{"ps -a", []string{"podman", "ps", "-a"}, "podman-ps.stdout", "stdout", []string{"sctxcov1", "alpine:3.20"}},
		{"images", []string{"podman", "images"}, "podman-images.stdout", "stdout", []string{"images"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := dockerNativeFixture(t, tc.fix)
			out, err := p.Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw)})
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.wants {
				if !strings.Contains(string(out.Body), w) {
					t.Errorf("missing %q in %q", w, out.Body)
				}
			}
			if len(out.Body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(out.Body), len(raw))
			}
		})
	}
}

func TestPodmanMachineFormatDeclines(t *testing.T) {
	_, err := NewPodman().Aggressive(context.Background(), format.Input{
		Argv: []string{"podman", "ps", "--format", "json"}, Stdout: strings.NewReader("[]"),
	})
	if err == nil {
		t.Fatal("--format must decline")
	}
}
