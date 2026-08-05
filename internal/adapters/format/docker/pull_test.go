package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const dockerPullFixture = "latest: Pulling from library/nginx\n" +
	"a2abf6c4d29d: Pulling fs layer\n" +
	"c7a4e4382001: Pulling fs layer\n" +
	"4044b9ba67c9: Waiting\n" +
	"a2abf6c4d29d: Downloading [==========>    ]  10.2MB/28.5MB\n" +
	"a2abf6c4d29d: Downloading [====================>]  25.4MB/28.5MB\n" +
	"a2abf6c4d29d: Verifying Checksum\n" +
	"a2abf6c4d29d: Download complete\n" +
	"a2abf6c4d29d: Extracting [====>    ]  2.621MB/28.5MB\n" +
	"a2abf6c4d29d: Pull complete\n" +
	"c7a4e4382001: Verifying Checksum\n" +
	"c7a4e4382001: Download complete\n" +
	"c7a4e4382001: Pull complete\n" +
	"4044b9ba67c9: Pull complete\n" +
	"Digest: sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab\n" +
	"Status: Downloaded newer image for nginx:latest\n" +
	"docker.io/library/nginx:latest\n"

const dockerPushFixture = "The push refers to repository [docker.io/user/myapp]\n" +
	"5f70bf18a086: Pushed\n" +
	"9f8a8f3d3c1a: Pushed\n" +
	"6a5c6b1a5c1a: Layer already exists\n" +
	"latest: digest: sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678 size: 1782\n"

func TestAggressivePullPush(t *testing.T) {
	f := New()

	t.Run("pull collapses per-layer progress, keeps digest and status", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "pull", "nginx"}, Stdout: strings.NewReader(dockerPullFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"latest: Pulling from library/nginx",
			"…+3 layers",
			"Digest: sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
			"Status: Downloaded newer image for nginx:latest",
			"docker.io/library/nginx:latest",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "Downloading") || strings.Contains(body, "Extracting") {
			t.Errorf("body still contains per-layer progress: %q", body)
		}
		if len(out.Body) >= len(dockerPullFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(dockerPullFixture))
		}
	})

	t.Run("push collapses per-layer progress, keeps digest line", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "push", "user/myapp"}, Stdout: strings.NewReader(dockerPushFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"The push refers to repository [docker.io/user/myapp]",
			"…+3 layers",
			"latest: digest: sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678 size: 1782",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "Pushed") {
			t.Errorf("body still contains per-layer Pushed lines: %q", body)
		}
	})

	t.Run("no layers is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "pull", "nginx"}, Stdout: strings.NewReader("Status: Image is up to date for nginx:latest\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
