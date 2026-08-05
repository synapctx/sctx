package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const dockerInspectFixture = `[
    {
        "Id": "abc123def456abc123def456abc123def456abc123def456",
        "Name": "/web",
        "Config": {
            "Image": "nginx:1.25",
            "Env": [
                "PATH=/usr/bin",
                "NODE_ENV=production"
            ]
        },
        "State": {
            "Status": "running",
            "Health": {
                "Status": "healthy"
            }
        },
        "NetworkSettings": {
            "Networks": {
                "bridge": {
                    "IPAddress": "172.17.0.2"
                }
            }
        }
    }
]
`

func TestAggressiveInspect(t *testing.T) {
	f := New()

	t.Run("delegates to jsoncompact and compacts pretty-printed JSON array", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "inspect", "web"}, Stdout: strings.NewReader(dockerInspectFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, `"Status":"running"`) {
			t.Errorf("body missing expected field, got: %q", body)
		}
		if len(out.Body) >= len(dockerInspectFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(dockerInspectFixture))
		}
	})

	t.Run("non-JSON custom format degrades", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "inspect", "-f", "{{.State.Status}}", "web"}, Stdout: strings.NewReader("running\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
