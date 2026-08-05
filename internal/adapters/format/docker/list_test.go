package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const dockerNetworkLsFixture = "NETWORK ID     NAME            DRIVER    SCOPE\n" +
	"a1b2c3d4e5f6   bridge          bridge    local\n" +
	"b2c3d4e5f6a1   host            host      local\n" +
	"c3d4e5f6a1b2   none            null      local\n" +
	"d4e5f6a1b2c3   myapp_default   bridge    local\n"

const dockerVolumeLsFixture = "DRIVER    VOLUME NAME\n" +
	"local     myapp_data\n" +
	"local     myapp_cache\n"

const dockerHistoryFixture = "IMAGE          CREATED         CREATED BY                                                                     SIZE\n" +
	"abc123def456   3 days ago      /bin/sh -c #(nop)  CMD [\"node\" \"server.js\"]                                     0B\n" +
	"def456abc789   3 days ago      /bin/sh -c #(nop)  EXPOSE 3000                                                  0B\n" +
	"111222333444   3 days ago      /bin/sh -c #(nop) COPY dir:abcdefabcdefabcdefabcdefabcdefabcdefabcdef in /app   1.2MB\n"

const dockerTopFixture = "UID    PID     PPID    C    STIME   TTY   TIME       CMD\n" +
	"root   1234    1200    0    10:00   ?     00:00:01   nginx: master process\n" +
	"nginx  1235    1234    0    10:00   ?     00:00:00   nginx: worker process\n"

func TestAggressiveNetworkLs(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "network", "ls"}, Stdout: strings.NewReader(dockerNetworkLsFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "4 networks") {
		t.Errorf("body missing summary, got: %q", body)
	}
	for _, want := range []string{"bridge bridge local", "myapp_default bridge local"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %q", want, body)
		}
	}
	if strings.Contains(body, "a1b2c3d4e5f6") {
		t.Errorf("body still contains network ID: %q", body)
	}
}

func TestAggressiveVolumeLs(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "volume", "ls"}, Stdout: strings.NewReader(dockerVolumeLsFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "2 volumes") {
		t.Errorf("body missing summary, got: %q", body)
	}
	for _, want := range []string{"myapp_data local", "myapp_cache local"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %q", want, body)
		}
	}
}

func TestAggressiveHistory(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "history", "myapp"}, Stdout: strings.NewReader(dockerHistoryFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "3 layers") {
		t.Errorf("body missing summary, got: %q", body)
	}
	if !strings.Contains(body, "…(+") {
		t.Errorf("body missing truncation marker for long CREATED BY, got: %q", body)
	}
	if strings.Contains(body, "abcdefabcdefabcdefabcdefabcdefabcdefabcdef") {
		t.Errorf("body did not truncate long CREATED BY: %q", body)
	}
}

func TestAggressiveTop(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "top", "web"}, Stdout: strings.NewReader(dockerTopFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "2 processes") {
		t.Errorf("body missing summary, got: %q", body)
	}
	for _, want := range []string{"1234 nginx: master process", "1235 nginx: worker process"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %q", want, body)
		}
	}
}

func TestAggressiveContainerLsRoutesToPs(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"docker", "container", "ls"}, Stdout: strings.NewReader(dockerPsFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.HasPrefix(string(out.Body), "3 containers (2 up)") {
		t.Errorf("body = %q, want ps-style summary", out.Body)
	}
}
