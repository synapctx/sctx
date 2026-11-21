package netinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, tier, argv, raw string, exit int) (format.Rendered, error) {
	t.Helper()
	args := strings.Fields(argv)
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == args[0] {
			f = x
		}
	}
	in := format.Input{Argv: args, Stdout: strings.NewReader(raw), ExitCode: exit}
	if tier == "relaxed" {
		return f.Relaxed(context.Background(), in)
	}
	return f.Aggressive(context.Background(), in)
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file, argv string
		keep, drop []string
	}{
		{"ip-addr", "ip addr",
			[]string{"1: lo UNKNOWN mtu 65536 127.0.0.1/8 ::1/128", "11: eth0@if252 UP mtu 65535 mac 42:ec:79:4a:5b:fe link-netnsid 0 10.0.0.2/16",
				"14: veth1@vpeer1 UP mtu 1500 master br0 mac fe:3e:6e:2b:c9:74 fe80::fc3e:6eff:fe2b:c974/64",
				"10.0.0.5/24 10.0.0.6/24(secondary)", "DOWN, no address, zero MAC ×9: tunl0@NONE gre0@NONE"},
			[]string{"valid_lft", "qdisc", "brd ", "group default", "mode DEFAULT"}},
		{"ip-br-addr", "ip -br addr",
			[]string{"eth0@if252 UP 10.0.0.2/16", "br0 UP 10.0.1.1/16 fe80::c454:53ff:fe68:86e3/64", "DOWN, nothing to show ×9:", "only fe80:: link-local address (omitted) ×24: vpeer1@veth1 veth1@vpeer1"},
			[]string{"fe80::34ad"}},
		{"ip-link", "ip link", []string{"14: veth1@vpeer1 UP mtu 1500 master br0 mac fe:3e:6e:2b:c9:74", "DOWN, no address, zero MAC ×9"}, []string{"qdisc", "mode DEFAULT"}},
		{"ip-br-link", "ip -br link", []string{"veth1@vpeer1 UP fe:3e:6e:2b:c9:74 <BROADCAST,MULTICAST,UP,LOWER_UP>", "DOWN, nothing to show ×9"}, nil},
		{"ss-tan", "ss -tan",
			[]string{"State Recv-Q Send-Q Local Address:Port Peer Address:Port Process", "LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*", "LISTEN 2 1 0.0.0.0:8001 0.0.0.0:*", "LISTEN 0 4096 [::]:22 [::]:*",
				"connections grouped by endpoint (100 rows):", "SYN-SENT out 127.0.0.1:8001 ×57 from/to 127.0.0.1"},
			[]string{"127.0.0.1:47070"}},
		{"ss-tanp", "ss -tanp",
			[]string{`LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=906,fd=3),("systemd",pid=1,fd=47))`, "SYN-SENT out 127.0.0.1:8001 ×57 from/to 127.0.0.1 (nc)",
				`ESTAB 0 0 127.0.0.1:8003 127.0.0.1:47844 users:(("nc",pid=1086,fd=4))`}, []string{"pid=1273"}},
		{"ss-tulpn", "ss -tulpn", []string{"udp UNCONN 0 0 127.0.0.54:53 0.0.0.0:*", `tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=906,fd=3),("systemd",pid=1,fd=47))`}, nil},
		{"netstat-an", "netstat -an",
			[]string{"Active Internet connections (servers and established)", "tcp 0 0 0.0.0.0:22 0.0.0.0:* LISTEN", "udp 0 0 127.0.0.54:53 0.0.0.0:*", "tcp SYN_SENT out 127.0.0.1:8001 ×57", "Active UNIX domain sockets (servers and established)"}, []string{"127.0.0.1:47070"}},
		{"netstat-tanp", "netstat -tanp", []string{"tcp 0 0 0.0.0.0:22 0.0.0.0:* LISTEN 1/init", "tcp SYN_SENT out 127.0.0.1:8001 ×57 from/to 127.0.0.1 (nc)", "tcp 0 0 127.0.0.1:8003 127.0.0.1:47844 ESTABLISHED 1086/nc"}, nil},
		{"traceroute-30", "traceroute -n 192.0.2.1", []string{"traceroute to 192.0.2.1", " 1  10.0.0.1  0.089 ms  0.012 ms  0.007 ms", "2-30  * (no reply) ×29"}, []string{" 7  * * *"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(t, "aggressive", tc.argv, raw, 0)
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
			// ss -tulpn only squeezes column padding, so nothing is elided.
			if !out.Elided && tc.file != "ss-tulpn" {
				t.Errorf("Elided must be set: content was folded")
			}
		})
	}
}

// Listeners are individual facts: however many connections are folded, every
// LISTEN/UNCONN row in the input must appear in the output.
func TestEveryListenerSurvivesGrouping(t *testing.T) {
	for _, tc := range []struct{ file, argv string }{
		{"ss-tan", "ss -tan"}, {"ss-tanp", "ss -tanp"}, {"netstat-an", "netstat -an"}, {"netstat-tanp", "netstat -tanp"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(t, "aggressive", tc.argv, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, l := range strings.Split(raw, "\n") {
				if !strings.Contains(l, "LISTEN") {
					continue
				}
				n++
				if !strings.Contains(string(out.Body), strings.Join(strings.Fields(l), " ")) && !strings.Contains(string(out.Body), l) {
					t.Errorf("listener dropped: %q", l)
				}
			}
			if n == 0 {
				t.Fatal("fixture has no listeners")
			}
		})
	}
}

// Every address of every interface survives the one-line rendering.
func TestIPAddrKeepsEveryAddress(t *testing.T) {
	raw := fixture(t, "ip-addr")
	out, err := run(t, "aggressive", "ip addr", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(raw, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && (f[0] == "inet" || f[0] == "inet6") {
			n++
			if !strings.Contains(string(out.Body), f[1]) {
				t.Errorf("address %s lost", f[1])
			}
		}
	}
	if n < 30 {
		t.Fatalf("expected a docker-host-sized fixture, saw %d addresses", n)
	}
}

func TestIPAddrKeepsNonForeverLease(t *testing.T) {
	raw := "2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000\n" +
		"    link/ether 52:54:00:12:34:56 brd ff:ff:ff:ff:ff:ff\n" +
		"    inet 10.0.0.9/24 metric 100 brd 10.0.0.255 scope global dynamic eth0\n" +
		"       valid_lft 86380sec preferred_lft 86380sec\n" +
		"    inet6 fe80::5054:ff:fe12:3456/64 scope link \n" +
		"       valid_lft forever preferred_lft forever\n" +
		strings.Repeat("3: tunl0@NONE: <NOARP> mtu 1480 qdisc noop state DOWN group default qlen 1000\n    link/ipip 0.0.0.0 brd 0.0.0.0\n", 4)
	out, err := run(t, "aggressive", "ip addr", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "10.0.0.9/24(metric 100 dynamic){valid=86380sec,pref=86380sec}") {
		t.Errorf("a finite lease must be reported:\n%s", out.Body)
	}
}

func TestDeclines(t *testing.T) {
	addr := fixture(t, "ip-addr")
	ss := fixture(t, "ss-tanp")
	for _, tc := range []struct{ argv, raw string }{
		{"ip -j addr", addr}, {"ip -json addr", addr}, {"ip -o addr", addr}, {"ip -s link", addr}, {"ip -d link", addr},
		{"ip route", addr}, {"ip neigh", addr}, {"ip rule", addr}, {"ip", addr},
		{"ss -H -tan", ss}, {"ss -tani", ss}, {"ss -s", ss}, {"ss --info -t", ss}, {"ss -c", ss}, {"ss -tanm", ss}, {"ss -tane", ss},
		{"netstat -rn", ss}, {"netstat -i", ss}, {"netstat -s", ss}, {"netstat -c", ss},
	} {
		t.Run(tc.argv, func(t *testing.T) {
			if _, err := run(t, "aggressive", tc.argv, tc.raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
	t.Run("small output", func(t *testing.T) {
		for _, c := range []struct{ file, argv string }{
			{"small-ip-route", "ip route"}, {"small-traceroute-n", "traceroute -n 1.1.1.1"}, {"small-ss-s", "ss -s"},
		} {
			if _, err := run(t, "aggressive", c.argv, fixture(t, c.file), 0); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("%s: want decline, got %v", c.argv, err)
			}
		}
	})
	t.Run("failure message only", func(t *testing.T) {
		raw := fixture(t, "small-ip-addr-bad-dev")
		if _, err := run(t, "aggressive", "ip addr show dev nosuch0", raw, 1); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("an error message must pass through untouched, got %v", err)
		}
	})
	t.Run("relaxed does not rewrite interfaces", func(t *testing.T) {
		if _, err := run(t, "relaxed", "ip addr", addr, 0); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
}

func TestStderrIsAppended(t *testing.T) {
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == "ss" {
			f = x
		}
	}
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"ss", "-tan"}, Stdout: strings.NewReader(fixture(t, "ss-tan")),
		Stderr: strings.NewReader("RTNETLINK answers: Invalid argument\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || !strings.HasSuffix(string(out.Body), "RTNETLINK answers: Invalid argument") {
		t.Errorf("stderr not folded: %q", out.Body)
	}
}

func TestTracerouteKeepsEveryAnsweringHop(t *testing.T) {
	var b strings.Builder
	b.WriteString("traceroute to 192.0.2.1 (192.0.2.1), 30 hops max, 60 byte packets\n 1  10.0.0.1  0.089 ms  0.012 ms  0.007 ms\n")
	for i := 2; i <= 9; i++ {
		fmt.Fprintf(&b, "%2d  * * *\n", i)
	}
	b.WriteString("10  10.9.9.9  12.0 ms  * 13.0 ms\n11  * * *\n12  192.0.2.1  20.1 ms  20.0 ms  19.9 ms\n")
	out, err := run(t, "aggressive", "traceroute -n 192.0.2.1", b.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"2-9  * (no reply) ×8", "10  10.9.9.9  12.0 ms  * 13.0 ms", "11  * * *", "12  192.0.2.1  20.1 ms  20.0 ms  19.9 ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("lost %q in:\n%s", want, body)
		}
	}
}
