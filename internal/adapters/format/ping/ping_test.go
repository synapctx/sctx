package ping

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		exit int
		keep []string
		drop []string
	}{
		{"ok.txt", 0,
			[]string{"PING 127.0.0.1 (127.0.0.1): 56 data bytes", "replies ×5: 64 bytes from 127.0.0.1 ttl=64 icmp_seq 0-4", "--- 127.0.0.1 ping statistics ---", "5 packets transmitted, 5 packets received, 0.0% packet loss", "round-trip min/avg/max/stddev = 0.085/0.111/0.154/0.023 ms"},
			[]string{"icmp_seq=1 "}},
		{"many.txt", 0,
			[]string{"replies ×12:", "icmp_seq 0-11", "12 packets transmitted, 12 packets received"}, []string{"icmp_seq=7"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ping", "-c", "3", "h"}, Stdout: strings.NewReader(raw), ExitCode: tc.exit})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller %d >= %d", len(body), len(raw))
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
		})
	}
}

func TestSmallOrErrorOutputDeclines(t *testing.T) {
	for _, f := range []string{"badhost.txt", "quiet.txt", "loss.txt"} {
		_, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ping", "-c", "2", "x"}, Stdout: strings.NewReader(fixture(t, f)), ExitCode: 68})
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestTimeoutsCollapseWithRange(t *testing.T) {
	var b strings.Builder
	b.WriteString("PING h (192.0.2.1): 56 data bytes\n")
	for i := 0; i < 6; i++ {
		b.WriteString("Request timeout for icmp_seq " + string(rune('0'+i)) + "\n")
	}
	b.WriteString("\n--- h ping statistics ---\n7 packets transmitted, 0 packets received, 100.0% packet loss\n")
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ping", "-c", "7", "h"}, Stdout: strings.NewReader(b.String()), ExitCode: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Request timeout ×6 (icmp_seq 0-5)", "100.0% packet loss"} {
		if !strings.Contains(string(out.Body), k) {
			t.Errorf("lost %q:\n%s", k, out.Body)
		}
	}
}

func TestOddRepliesPassThroughVerbatim(t *testing.T) {
	raw := "PING h (1.1.1.1): 56 data bytes\n" +
		"64 bytes from 1.1.1.1: icmp_seq=0 ttl=64 time=1.0 ms\n" +
		"64 bytes from 1.1.1.1: icmp_seq=1 ttl=64 time=1.1 ms\n" +
		"64 bytes from 1.1.1.1: icmp_seq=1 ttl=64 time=1.1 ms (DUP!)\n" +
		"92 bytes from 10.0.0.1: Time to live exceeded\n" +
		"64 bytes from 1.1.1.1: icmp_seq=2 ttl=64 time=1.2 ms\n" +
		"64 bytes from 1.1.1.1: icmp_seq=3 ttl=64 time=1.2 ms\n" +
		"\n--- h ping statistics ---\n4 packets transmitted, 4 packets received, +1 duplicates, 0.0% packet loss\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"ping", "-c", "4", "h"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"(DUP!)", "Time to live exceeded", "+1 duplicates", "replies ×4"} {
		if !strings.Contains(string(out.Body), k) {
			t.Errorf("lost %q:\n%s", k, out.Body)
		}
	}
}
