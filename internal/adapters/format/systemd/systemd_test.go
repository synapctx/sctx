package systemd

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile("testdata/" + name + ".exit")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(string(e)))
	return string(b), code
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
		{"systemctl-status-web", "systemctl status web", []string{"host: af8069ba3d96", "● web.service - Demo web service", "Active: active (running)", "Main PID: 134 (sh)", "handling request id= status=200 ×10"}, []string{"af8069ba3d96 sh[134]"}},
		{"systemctl-status-broken", "systemctl status broken", []string{"× broken.service", "Active: failed (Result: exit-code)", "status=1/FAILURE", "Failed to start broken.service - Demo broken worker.", "Start request repeated too quickly."}, nil},
		{"systemctl-status-pool", "systemctl status pool", []string{"Main PID: 2350 (sh)", "…+7 more CGroup rows", "pool master ready"}, []string{"Docs:", "man:pool(8)", "├─2357"}},
		{"systemctl-status-app", "systemctl status app", []string{"processed batch 35 in 245 ms", "…+4 similar lines (digits vary)", "processed batch 40 in 280 ms", "ERROR: upstream timeout after 30s (attempt 1)", "ERROR: upstream timeout after 30s (attempt 2)"}, []string{"batch 37"}},
		{"systemctl-status-multi", "systemctl status web broken pool", []string{"● web.service", "× broken.service", "● pool.service"}, nil},
		{"systemctl-list-units", "systemctl list-units",
			[]string{"● broken.service loaded failed failed Demo broken worker", "dev-vda1.device loaded activating tentative /dev/vda1", "active/running service ×9: app dbus", "active/exited service ×17:", "81 loaded units listed", "…+83 omitted"}, []string{"Legend:", "D-Bus System Message Bus", "Rebuild Dynamic Linker Cache"}},
		{"systemctl-list-units-all", "systemctl list-units --all", []string{"● broken.service loaded failed failed", "● NetworkManager.service not-found inactive dead", "inactive/dead service ×51:", "184 loaded units listed"}, []string{"Legend:"}},
		{"systemctl-list-units-nolegend", "systemctl list-units --type=service --no-legend", []string{"● broken.service loaded failed failed", "active/running service ×9:"}, nil},
		{"systemctl-failed", "systemctl --failed", []string{"● broken.service loaded failed failed Demo broken worker", "1 loaded units listed."}, []string{"Legend:", "UNIT "}},
		{"systemctl-list-unit-files", "systemctl list-unit-files", []string{"static ×172 (names omitted; list with --state=static)", "enabled ×18: e2scrub_reap.service", "ssh.service(preset:enabled)", "masked ×4: cryptdisks-early.service", "252 unit files listed."}, []string{"UNIT FILE"}},
		{"journalctl-app", "journalctl -u app -n 100 --no-pager", []string{"host: af8069ba3d96", "processed batch 1 in 7 ms", "…+38 similar lines (digits vary)", "processed batch 40 in 280 ms", "ERROR: upstream timeout after 30s (attempt 2)", "listening on :8080"}, []string{"batch 20 "}},
		{"journalctl-iso", "journalctl -u app -o short-iso", []string{"2026-11-21T02:42:50+00:00 sh[2348]: listening on :8080", "…+38 similar lines"}, nil},
		{"journalctl-web", "journalctl -u web -n 300 --no-pager", []string{"Nov 21 02:38:05…02:43:04 sh[134]: handling request id= status=200 ×300"}, nil},
		{"journalctl-broken", "journalctl -u broken --no-pager", []string{"FATAL: password authentication failed for user app", "status=1/FAILURE", "restart counter is at 4", "Unknown key name 'StartLimitIntervalSec' in section 'Service', ignoring. ×5"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, exit := fixture(t, tc.file)
			out, err := run(t, "aggressive", tc.argv, raw, exit)
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
					t.Errorf("kept %q in:\n%s", d, body)
				}
			}
		})
	}
}

// TestErrorLinesSurviveEveryTier: no journal/status line that signals a failure
// may be dropped or digit-folded, in either tier.
func TestErrorLinesSurviveEveryTier(t *testing.T) {
	cases := []struct{ file, argv string }{
		{"journalctl-broken", "journalctl -u broken --no-pager"},
		{"journalctl-app", "journalctl -u app --no-pager"},
		{"systemctl-status-broken", "systemctl status broken"},
		{"systemctl-status-multi", "systemctl status web broken pool"},
		{"journalctl-boot", "journalctl -b --no-pager"},
		{"journalctl-k", "journalctl -k --no-pager"},
	}
	for _, tc := range cases {
		for _, tier := range []string{"aggressive", "relaxed"} {
			t.Run(tc.file+"/"+tier, func(t *testing.T) {
				raw, exit := fixture(t, tc.file)
				out, err := run(t, tier, tc.argv, raw, exit)
				if errors.Is(err, format.ErrTierInapplicable) {
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				body := string(out.Body)
				n := 0
				for _, l := range strings.Split(raw, "\n") {
					m := journalRE.FindStringSubmatch(l)
					if m == nil || !errorishRE.MatchString(m[3]) {
						continue
					}
					n++
					// the message must survive, possibly as the representative of an exact ×N run
					if !strings.Contains(body, m[3]) {
						t.Errorf("lost error message %q", m[3])
					}
				}
				if n == 0 {
					t.Skip("fixture has no error line")
				}
			})
		}
	}
}

func TestNonErrorDigitFoldNeverEatsDistinctExitCodes(t *testing.T) {
	var b strings.Builder
	b.WriteString(strings.Repeat("Nov 21 02:30:34 h svc[1]: tick\n", 6))
	for _, c := range []string{"1", "2", "137", "139", "1", "2", "3"} {
		b.WriteString("Nov 21 02:30:35 h svc[1]: worker finished, exit status=" + c + "\n")
	}
	out, err := run(t, "aggressive", "journalctl -u svc", b.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"status=137", "status=139"} {
		if !strings.Contains(string(out.Body), c) {
			t.Errorf("distinct exit code %s was folded away:\n%s", c, out.Body)
		}
	}
}

func TestMixedHostsKeepHostColumn(t *testing.T) {
	raw := "Nov 21 02:30:35 alpha app[1]: ready\nNov 21 02:30:36 beta app[1]: ready\n" +
		strings.Repeat("Nov 21 02:30:37 alpha app[1]: tick\n", 6)
	out, err := run(t, "aggressive", "journalctl --no-pager", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if strings.Contains(body, "host:") || !strings.Contains(body, "beta app[1]: ready") || !strings.Contains(body, "alpha app[1]: tick ×6") {
		t.Errorf("host column must survive when hosts differ:\n%s", body)
	}
}

func TestRelaxedDoesNotFoldDigitsOrStripHost(t *testing.T) {
	raw, exit := fixture(t, "journalctl-app")
	out, err := run(t, "relaxed", "journalctl -u app", raw, exit)
	if err != nil {
		// nothing exactly identical in this fixture is acceptable
		if errors.Is(err, format.ErrTierInapplicable) {
			return
		}
		t.Fatal(err)
	}
	if strings.Contains(string(out.Body), "similar lines") || strings.Contains(string(out.Body), "host:") {
		t.Errorf("relaxed applied an aggressive rule:\n%s", out.Body)
	}
}

func TestDeclines(t *testing.T) {
	raw, _ := fixture(t, "journalctl-web")
	for _, argv := range []string{
		"journalctl -u web -o json", "journalctl -u web --output=json-pretty", "journalctl -ojson", "journalctl -o export", "journalctl --disk-usage",
		"journalctl --list-boots", "journalctl -F _SYSTEMD_UNIT", "journalctl --vacuum-size=100M",
		"systemctl restart web", "systemctl is-active web", "systemctl show web", "systemctl list-timers", "systemctl status -o json web",
		"systemctl", "systemctl status --json=pretty web", "systemctl daemon-reload",
	} {
		t.Run(argv, func(t *testing.T) {
			if _, err := run(t, "aggressive", argv, raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
	t.Run("status of a missing unit has no block", func(t *testing.T) {
		missing, exit := fixture(t, "systemctl-status-missing")
		if _, err := run(t, "aggressive", "systemctl status nosuch", missing, exit); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("no log lines", func(t *testing.T) {
		out, exit := fixture(t, "journalctl-nolog")
		if _, err := run(t, "aggressive", "journalctl -u nosuch", out, exit); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("-o cat has no timestamps", func(t *testing.T) {
		c, exit := fixture(t, "journalctl-cat")
		if _, err := run(t, "aggressive", "journalctl -u app -o cat", c, exit); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
}

func TestStderrIsAppendedVerbatim(t *testing.T) {
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == "journalctl" {
			f = x
		}
	}
	raw, _ := fixture(t, "journalctl-web")
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"journalctl", "-u", "web"},
		Stdout: strings.NewReader(raw), Stderr: strings.NewReader("Failed to get journal access: Permission denied\n"), ExitCode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || !strings.HasSuffix(string(out.Body), "Failed to get journal access: Permission denied") {
		t.Errorf("stderr not folded verbatim: %v %q", out.FoldStderr, out.Body)
	}
}
