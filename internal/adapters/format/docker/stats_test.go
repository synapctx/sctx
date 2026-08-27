package docker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const dockerStatsFixture = "CONTAINER ID   NAME      CPU %     MEM USAGE / LIMIT     MEM %     NET I/O           BLOCK I/O         PIDS\n" +
	"a1b2c3d4e5f6   web       0.15%     45.2MiB / 1.943GiB    2.27%     1.2kB / 0B        0B / 0B           5\n" +
	"b2c3d4e5f6a1   db        1.23%     128.5MiB / 1.943GiB   6.45%     3.4kB / 2.1kB     12.3MB / 0B       10\n"

func TestAggressiveStats(t *testing.T) {
	f := New()

	t.Run("no-stream table collapses to name cpu mem lines", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "stats", "--no-stream"}, Stdout: strings.NewReader(dockerStatsFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"2 containers",
			"web cpu=0.15% mem=45.2MiB / 1.943GiB(2.27%)",
			"db cpu=1.23% mem=128.5MiB / 1.943GiB(6.45%)",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "a1b2c3d4e5f6") {
			t.Errorf("body still contains container ID: %q", body)
		}
	})

	t.Run("caps rows with elision marker", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("CONTAINER ID   NAME      CPU %     MEM USAGE / LIMIT     MEM %     NET I/O    BLOCK I/O   PIDS\n")
		for i := range 25 {
			fmt.Fprintf(&b, "%012d   c%d       0.10%%     10MiB / 1GiB          1.00%%     0B / 0B    0B / 0B     1\n", i, i)
		}
		in := format.Input{Argv: []string{"docker", "stats", "--no-stream"}, Stdout: strings.NewReader(b.String())}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "…+5 more") {
			t.Errorf("body missing cap marker, got: %q", out.Body)
		}
	})

	t.Run("without --no-stream degrades (streaming, not a static table)", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "stats"}, Stdout: strings.NewReader(dockerStatsFixture)}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
