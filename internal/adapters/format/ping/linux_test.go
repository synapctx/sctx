package ping

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Fixtures: linux-gnu-* are iputils ping on ubuntu:24.04 ("icmp_seq=N",
// "rtt min/avg/max/mdev"); linux-busybox-* are busybox ping on alpine:3.20
// ("seq=N", "N packets received", "round-trip min/avg/max").

func linux(t *testing.T, distro, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/linux-" + distro + "-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile("testdata/linux-" + distro + "-" + name + ".exit")
	if err != nil {
		t.Fatal(err)
	}
	return string(b), int(e[0] - '0')
}

func TestLinuxFixtures(t *testing.T) {
	cases := []struct {
		distro, name string
		render       bool
		keep         []string
	}{
		{"gnu", "ping-c5", true, []string{"PING 127.0.0.1 (127.0.0.1) 56(84) bytes of data.", "replies ×5: 64 bytes from 127.0.0.1 ttl=64 icmp_seq 1-5", "5 packets transmitted, 5 received, 0% packet loss", "rtt min/avg/max/mdev"}},
		{"gnu", "ping-c10", true, []string{"replies ×10:", "icmp_seq 1-10", "10 packets transmitted, 10 received"}},
		// busybox spells the sequence "seq=N": the reply regex used to miss it,
		// so the replies passed through unrendered.
		{"busybox", "ping-c5", true, []string{"PING 127.0.0.1 (127.0.0.1): 56 data bytes", "replies ×5: 64 bytes from 127.0.0.1 ttl=64 icmp_seq 0-4", "5 packets transmitted, 5 packets received, 0% packet loss", "round-trip min/avg/max"}},
		{"busybox", "ping-c10", true, []string{"replies ×10:", "icmp_seq 0-9", "10 packets transmitted, 10 packets received"}},
		{"gnu", "ping-bad", false, []string{"Name or service not known"}},
		{"busybox", "ping-bad", false, []string{"bad address 'nosuch.invalid'"}},
		{"gnu", "ping-unreach", false, []string{"100% packet loss"}},
		{"busybox", "ping-unreach", false, []string{"100% packet loss"}},
	}
	for _, c := range cases {
		t.Run(c.distro+"/"+c.name, func(t *testing.T) {
			raw, exit := linux(t, c.distro, c.name)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ping", "-c", "5", "h"}, Stdout: strings.NewReader(raw), ExitCode: exit})
			rendered := err == nil
			if rendered != c.render {
				t.Fatalf("rendered=%v (err %v), want %v", rendered, err, c.render)
			}
			shown := raw
			if rendered {
				shown = string(out.Body)
				if len(shown) >= len(raw) {
					t.Errorf("not smaller: %d >= %d", len(shown), len(raw))
				}
			}
			for _, k := range c.keep {
				if !strings.Contains(shown, k) {
					t.Errorf("lost %q:\n%s", k, shown)
				}
			}
		})
	}
}

// iputils -O prints "no answer yet for icmp_seq=N" for lost probes.
func TestIputilsNoAnswerCollapses(t *testing.T) {
	var b strings.Builder
	b.WriteString("PING 192.0.2.1 (192.0.2.1) 56(84) bytes of data.\n")
	for i := 1; i <= 5; i++ {
		b.WriteString("no answer yet for icmp_seq=" + string(rune('0'+i)) + "\n")
	}
	b.WriteString("\n--- 192.0.2.1 ping statistics ---\n5 packets transmitted, 0 received, 100% packet loss, time 4090ms\n")
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ping", "-O", "-c", "5", "192.0.2.1"}, Stdout: strings.NewReader(b.String()), ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Request timeout ×5 (icmp_seq 1-5)", "100% packet loss"} {
		if !strings.Contains(string(out.Body), k) {
			t.Errorf("lost %q:\n%s", k, out.Body)
		}
	}
}
