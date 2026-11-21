package fw

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
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, tier, argv, raw string) (format.Rendered, error) {
	t.Helper()
	args := strings.Fields(argv)
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == args[0] {
			f = x
		}
	}
	in := format.Input{Argv: args, Stdout: strings.NewReader(raw)}
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
		{"iptables-L", "iptables -L -n -v",
			[]string{"columns: pkts bytes target prot [opt] [in=] [out=] [src=] [dst=] options", "Chain INPUT (policy DROP 0 packets, 0 bytes)", "0 0 ACCEPT 6 tcp dpt:22", "494 29688 ACCEPT 0 in=lo",
				"0 0 DROP 0 src=192.0.2.0/24", "0 0 ACCEPT 6 in=!br0 out=br0 dst=10.0.1.82 tcp dpt:80", "Chain ufw-after-forward (1 references): empty", `0 0 LOG 0 limit: avg 3/min burst 10 LOG flags 0 level 4 prefix "[UFW BLOCK] "`},
			[]string{" pkts bytes target     prot", "0.0.0.0/0"}},
		{"iptables-L-n", "iptables -L -n", []string{"columns:", "Chain DOCKER (1 references)", "ACCEPT 6 tcp dpt:22"}, []string{"0.0.0.0/0"}},
		{"iptables-L-line", "iptables -L -n -v --line-numbers", []string{"num pkts bytes target prot opt in out source destination", "1 0 0 ACCEPT 6 tcp dpt:22", "2 494 29688 ACCEPT 0 in=lo"}, []string{"0.0.0.0/0"}},
		{"iptables-nat", "iptables -t nat -L -n -v", []string{"Chain PREROUTING (policy ACCEPT 0 packets, 0 bytes)", "Chain POSTROUTING (policy ACCEPT 83 packets, 5004 bytes)", "ADDRTYPE match dst-type LOCAL"}, nil},
		{"ufw-status", "ufw status verbose", []string{"Status: active", "Default: deny (incoming), allow (outgoing), deny (routed)", "22/tcp  ALLOW IN  Anywhere", "5432/tcp  ALLOW IN  10.0.0.0/24", "22/tcp (v6)  ALLOW IN  Anywhere (v6)"}, []string{"Anywhere                  "}},
		{"ufw-numbered", "ufw status numbered", []string{"[ 1] 22/tcp  ALLOW IN  Anywhere", "[ 4] 22/tcp (v6)  ALLOW IN  Anywhere (v6)"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(t, "aggressive", tc.argv, raw)
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
		})
	}
}

// No rule loses a counter, target, protocol, interface, address or option: every
// non-wildcard token of every raw rule appears on the same output line.
func TestIptablesNeverDropsANonWildcardToken(t *testing.T) {
	for _, file := range []string{"iptables-L", "iptables-L-n", "iptables-nat", "iptables-L-line"} {
		t.Run(file, func(t *testing.T) {
			raw := fixture(t, file)
			out, err := run(t, "aggressive", "iptables -L -n -v", raw)
			if err != nil {
				t.Fatal(err)
			}
			outLines := strings.Split(string(out.Body), "\n")
			rules := 0
			for _, l := range strings.Split(raw, "\n") {
				f := strings.Fields(l)
				if len(f) < 5 || strings.HasPrefix(l, "Chain") || strings.Contains(l, "target") {
					continue
				}
				rules++
				found := false
				for _, ol := range outLines {
					ok := true
					for _, tok := range f {
						if tok == "--" || tok == "*" || isAny(tok) {
							continue
						}
						if !strings.Contains(" "+ol+" ", " "+tok+" ") && !strings.Contains(ol, "="+tok) {
							ok = false
							break
						}
					}
					if ok {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("rule lost a token: %q", l)
				}
			}
			if rules < 5 {
				t.Fatalf("fixture too small: %d rules", rules)
			}
		})
	}
}

func TestEveryChainHeaderSurvives(t *testing.T) {
	raw := fixture(t, "iptables-L")
	out, err := run(t, "aggressive", "iptables -L -n -v", raw)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, "Chain ") {
			n++
			if !strings.Contains(string(out.Body), strings.TrimSpace(l)) {
				t.Errorf("chain header lost: %q", l)
			}
		}
	}
	if n < 20 {
		t.Fatalf("expected many chains, saw %d", n)
	}
}

func TestRelaxedOnlySqueezes(t *testing.T) {
	raw := fixture(t, "iptables-L")
	out, err := run(t, "relaxed", "iptables -L -n -v", raw)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "0 0 ACCEPT 6 -- * * 0.0.0.0/0 0.0.0.0/0 tcp dpt:22") || strings.Contains(body, "[opt]") || strings.Contains(body, ": empty") {
		t.Errorf("relaxed must keep every column:\n%s", body[:600])
	}
}

func TestDeclines(t *testing.T) {
	raw := fixture(t, "iptables-L")
	for _, argv := range []string{
		"iptables -S", "iptables --list-rules", "iptables -A INPUT -j DROP", "iptables -I INPUT 1 -j DROP", "iptables -D INPUT 1", "iptables -F", "iptables -C INPUT -j DROP",
		"iptables", "iptables -V", "ufw allow 22", "ufw enable", "ufw show raw", "ufw", "ufw status-bogus",
	} {
		t.Run(argv, func(t *testing.T) {
			if _, err := run(t, "aggressive", argv, raw); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
	t.Run("error text only", func(t *testing.T) {
		bad := fixture(t, "iptables-bad")
		if _, err := run(t, "aggressive", "iptables -L -t nosuch", bad); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("ufw inactive", func(t *testing.T) {
		if _, err := run(t, "aggressive", "ufw status verbose", "Status: inactive\n"); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
}

func TestMixedLayoutsAreNotGuessed(t *testing.T) {
	raw := "Chain INPUT (policy ACCEPT 0 packets, 0 bytes)\n pkts bytes target     prot opt in     out     source               destination\n    0     0 ACCEPT     6    --  *      *       0.0.0.0/0            0.0.0.0/0\n" +
		"\nChain FORWARD (policy ACCEPT)\ntarget     prot opt source               destination\nACCEPT     6    --  0.0.0.0/0            0.0.0.0/0\n"
	if _, err := run(t, "aggressive", "iptables -L -n", raw); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("a listing with two column layouts must decline, got %v", err)
	}
}

func TestStderrIsAppended(t *testing.T) {
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == "iptables" {
			f = x
		}
	}
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"iptables", "-L", "-n", "-v"}, Stdout: strings.NewReader(fixture(t, "iptables-L")),
		Stderr: strings.NewReader("# Warning: iptables-legacy tables present, use iptables-legacy to see them\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || !strings.HasSuffix(string(out.Body), "use iptables-legacy to see them") {
		t.Errorf("stderr lost")
	}
}
