package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const composePsFixture = `NAME            IMAGE          COMMAND                  SERVICE   CREATED        STATUS                  PORTS
myapp-web-1     myapp-web      "node server.js"         web       3 hours ago    Up 3 hours              0.0.0.0:3000->3000/tcp
myapp-db-1      postgres:16    "docker-entrypoint.s…"   db        3 hours ago    Exited (1) 2 hours ago
`

func TestAggressiveComposePs(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "compose", "ps"}, Stdout: strings.NewReader(composePsFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "2 containers (1 up)") {
		t.Errorf("body missing summary, got: %q", body)
	}
	for _, want := range []string{"web up 0.0.0.0:3000->3000/tcp", "db exit(1)"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %q", want, body)
		}
	}
	if strings.Contains(body, "myapp-web-1") {
		t.Errorf("body still contains container name column: %q", body)
	}
}

const composeUpFixture = "[+] Running 4/4\n" +
	" ✔ Network myapp_default        Created                                        0.1s\n" +
	" ✔ Volume \"myapp_data\"          Created                                        0.0s\n" +
	" ✔ Container myapp-db-1         Started                                        0.3s\n" +
	" ✔ Container myapp-web-1        Started                                        0.4s\n" +
	"Attaching to myapp-db-1, myapp-web-1\n" +
	"myapp-db-1   | 2024-01-01 ready to accept connections\n" +
	"myapp-web-1  | Server listening on port 3000\n"

const composeUpWithPullFixture = "myapp-web Pulling\n" +
	"myapp-web Pulled\n" +
	"myapp-db Pulling\n" +
	"myapp-db Pulled\n" +
	"[+] Running 2/2\n" +
	" ✔ Container myapp-db-1   Started   0.3s\n" +
	" ✔ Container myapp-web-1  Started   0.4s\n"

func TestAggressiveComposeUp(t *testing.T) {
	f := New()

	t.Run("compacts resource progress and keeps attached log stream", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "compose", "up"}, Stdout: strings.NewReader(composeUpFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"Network myapp_default: created",
			"Volume myapp_data: created",
			"Container myapp-db-1: started",
			"Container myapp-web-1: started",
			"Attaching to myapp-db-1, myapp-web-1",
			"myapp-db-1   | 2024-01-01 ready to accept connections",
			"myapp-web-1  | Server listening on port 3000",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
	})

	t.Run("collapses per-service pull progress", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "compose", "up", "-d"}, Stdout: strings.NewReader(composeUpWithPullFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "…+4 pull lines") {
			t.Errorf("body missing pull-collapse marker, got: %q", body)
		}
		if strings.Contains(body, "Pulling") || strings.Contains(body, "Pulled") {
			t.Errorf("body still contains raw pull lines: %q", body)
		}
		if !strings.Contains(body, "Container myapp-db-1: started") {
			t.Errorf("body missing container start line: %q", body)
		}
	})
}

const composeDownFixture = "Stopping myapp-web-1 ... done\n" +
	"Stopping myapp-db-1 ... done\n" +
	"Removing myapp-web-1 ... done\n" +
	"Removing myapp-db-1 ... done\n" +
	"Removing network myapp_default\n"

func TestAggressiveComposeDown(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "compose", "down"}, Stdout: strings.NewReader(composeDownFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "5 removals") {
		t.Errorf("body missing summary, got: %q", body)
	}
	if !strings.Contains(body, "Removing network myapp_default") {
		t.Errorf("body missing network removal line, got: %q", body)
	}
}

func TestAggressiveComposeLogsRoutesToLogs(t *testing.T) {
	f := New()
	fixture := "web-1  | GET / 200 15ms\ndb-1   | ready to accept connections\nweb-1  | GET /api 200 8ms\n"
	in := format.Input{Argv: []string{"docker", "compose", "logs"}, Stdout: strings.NewReader(fixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "GET / 200 15ms") {
		t.Errorf("body missing log content, got: %q", out.Body)
	}
}

func TestAggressiveComposeBuildRoutesToBuild(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "compose", "build"}, Stdout: strings.NewReader(buildKitPlainFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.HasPrefix(string(out.Body), "6 build steps") {
		t.Errorf("body = %q, want build-step summary", out.Body)
	}
}
