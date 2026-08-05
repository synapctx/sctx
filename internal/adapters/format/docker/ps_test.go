package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const dockerPsFixture = `CONTAINER ID   IMAGE          COMMAND                  CREATED        STATUS                     PORTS                    NAMES
a1b2c3d4e5f6   nginx:1.25     "nginx -g 'daemon of…"   3 hours ago    Up 3 hours (healthy)       0.0.0.0:8080->80/tcp     web
b2c3d4e5f6a1   postgres:16    "docker-entrypoint.s…"   2 days ago     Up 2 days                  5432/tcp                 db
c3d4e5f6a1b2   redis:7        "redis-server"           1 hour ago     Exited (1) 2 hours ago                              cache
`

const dockerPsEmptyFixture = `CONTAINER ID   IMAGE     COMMAND   CREATED   STATUS    PORTS    NAMES
`

func TestAggressivePs(t *testing.T) {
	f := New()

	t.Run("renders name state image ports with up summary", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "ps"}, Stdout: strings.NewReader(dockerPsFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "3 containers (2 up)") {
			t.Errorf("body missing summary, got: %q", body)
		}
		for _, want := range []string{
			"web up·healthy nginx:1.25 0.0.0.0:8080->80/tcp",
			"db up postgres:16 5432/tcp",
			"cache exit(1) redis:7",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "a1b2c3d4e5f6") {
			t.Errorf("body still contains container ID: %q", body)
		}
		if len(out.Body) >= len(dockerPsFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(dockerPsFixture))
		}
	})

	t.Run("header only renders 0 containers", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "ps"}, Stdout: strings.NewReader(dockerPsEmptyFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if got := string(out.Body); got != "0 containers" {
			t.Errorf("body = %q, want %q", got, "0 containers")
		}
	})

	t.Run("empty stdout renders 0 containers", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "ps"}, Stdout: strings.NewReader("")}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if got := string(out.Body); got != "0 containers" {
			t.Errorf("body = %q, want %q", got, "0 containers")
		}
	})
}

func TestCompactStatus(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"Up 3 hours (healthy)", "up·healthy"},
		{"Up 3 hours (unhealthy)", "up·unhealthy"},
		{"Up 2 seconds (health: starting)", "up"},
		{"Up 3 hours", "up"},
		{"Exited (1) 2 hours ago", "exit(1)"},
		{"Exited (137) 5 minutes ago", "exit(137)"},
		{"Created", "created"},
		{"Restarting (1) 3 seconds ago", "restarting"},
		{"Paused", "paused"},
	}
	for _, tc := range tests {
		if got := compactStatus(tc.status); got != tc.want {
			t.Errorf("compactStatus(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}
