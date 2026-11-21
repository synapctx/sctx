package rediscli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func load(t *testing.T, name string) (stdout, stderr string, exit int) {
	t.Helper()
	read := func(ext string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", "redis7_"+name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	exit, err := strconv.Atoi(strings.TrimSpace(read("exit")))
	if err != nil {
		t.Fatal(err)
	}
	return read("stdout"), read("stderr"), exit
}

func run(t *testing.T, name string, argv ...string) (format.Rendered, string, error) {
	t.Helper()
	stdout, stderr, exit := load(t, name)
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: append([]string{"redis-cli"}, argv...), Stdout: strings.NewReader(stdout),
		Stderr: strings.NewReader(stderr), ExitCode: exit,
	})
	return out, stdout + stderr, err
}

func TestInfoLosesOnlyLineEndingsAndBlankLines(t *testing.T) {
	out, raw, err := run(t, "info", "-h", "redis", "INFO")
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if strings.Contains(body, "\r") || strings.Contains(body, "\n\n") || out.Elided {
		t.Errorf("not normalised or marked elided")
	}
	for _, l := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if l != "" && !strings.Contains(body, l+"\n") {
			t.Fatalf("line lost: %q", l)
		}
	}
	if len(body) >= len(raw) {
		t.Errorf("not smaller")
	}
	if _, _, err := run(t, "info_mem", "INFO", "memory"); err != nil {
		t.Errorf("INFO memory: %v", err)
	}
}

func TestLongListsAreCappedWithMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		last string
	}{
		{"keys", []string{"KEYS", "user:*"}, "user:"},
		{"scanall", []string{"--scan", "--pattern", "h:*"}, "h:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, raw, err := run(t, tc.name, tc.argv...)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			lines := strings.Split(strings.TrimSpace(body), "\n")
			if len(lines) != lineHead+1+lineTail || lines[lineHead] != "…+1800 redis lines" || !out.Elided {
				t.Fatalf("lines=%d marker=%q elided=%v", len(lines), lines[lineHead], out.Elided)
			}
			rawLines := strings.Split(strings.TrimSpace(raw), "\n")
			if lines[0] != rawLines[0] || lines[len(lines)-1] != rawLines[len(rawLines)-1] {
				t.Error("edges changed")
			}
		})
	}
}

func TestShortOrScalarRepliesDecline(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"lrange5", []string{"LRANGE", "q", "0", "4"}},
		{"get", []string{"GET", "user:7"}},
		{"mget", []string{"MGET", "user:1"}},
		{"scan", []string{"SCAN", "0"}},
		{"tty", []string{"--no-raw", "LRANGE", "q", "0", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := run(t, tc.name, tc.argv...); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// redis-cli reports Redis errors on stdout with exit 0; connection failures
// exit 1. Neither may be reshaped, and the password notice is the only
// stderr line that may disappear.
func TestErrorSignalSurvives(t *testing.T) {
	for _, name := range []string{"wrongtype", "unknown", "args"} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := run(t, name, "LRANGE", "x", "0", "-1"); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("error reply on stdout must not be rewritten: %v", err)
			}
		})
	}
	for _, name := range []string{"refused", "refused2", "auth"} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, exit := load(t, name)
			in := func() format.Input {
				return format.Input{Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
			}
			if exit != 0 {
				if _, err := New().Aggressive(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("aggressive on failure: %v", err)
				}
			}
			out, err := New().Relaxed(context.Background(), in())
			if err != nil {
				if name == "auth" {
					t.Fatal(err)
				}
				return // no notice to strip: verbatim
			}
			body := string(out.Body)
			if strings.Contains(body, "Using a password") {
				t.Error("notice kept")
			}
			if name == "auth" && (!strings.Contains(body, "AUTH failed: ERR AUTH") || !strings.Contains(body, "PONG")) {
				t.Errorf("auth failure lost: %q", body)
			}
		})
	}
}

func TestCommandAndInteractive(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		cmd  string
		long bool
	}{
		{[]string{"redis-cli"}, "", true},
		{[]string{"redis-cli", "-h", "cache", "-p", "6380"}, "", true},
		{[]string{"redis-cli", "-h", "cache", "get", "k"}, "GET", false},
		{[]string{"redis-cli", "-n", "2", "-a", "pw", "INFO", "memory"}, "INFO", false},
		{[]string{"redis-cli", "MONITOR"}, "MONITOR", true},
		{[]string{"redis-cli", "-h", "c", "subscribe", "ch"}, "SUBSCRIBE", true},
		{[]string{"redis-cli", "PSUBSCRIBE", "a*"}, "PSUBSCRIBE", true},
		{[]string{"redis-cli", "--stat"}, "", true},
		{[]string{"redis-cli", "--latency"}, "", true},
		{[]string{"redis-cli", "--latency-history", "-i", "1"}, "", true},
		{[]string{"redis-cli", "-r", "-1", "-i", "1", "INFO"}, "INFO", true},
		{[]string{"redis-cli", "--scan", "--pattern", "u:*"}, "", false},
		{[]string{"redis-cli", "--bigkeys"}, "", false},
		{[]string{"redis-cli", "--pipe"}, "", false},
		{[]string{"redis-cli", "--lru-test", "100000"}, "", true},
		{[]string{"redis-cli", "--cluster", "create", "a:1", "b:2"}, "CREATE", true},
		{[]string{"redis-cli", "--cluster", "fix", "a:1"}, "FIX", true},
		{[]string{"redis-cli", "--cluster", "reshard", "a:1"}, "RESHARD", true},
		{[]string{"redis-cli", "--cluster", "rebalance", "a:1"}, "REBALANCE", true},
		{[]string{"redis-cli", "--cluster", "create", "a:1", "--cluster-yes"}, "CREATE", false},
		{[]string{"redis-cli", "--cluster", "check", "a:1"}, "CHECK", false},
	} {
		if got := Command(tc.argv); got != tc.cmd {
			t.Errorf("Command(%v) = %q, want %q", tc.argv, got, tc.cmd)
		}
		if got := Interactive(tc.argv); got != tc.long {
			t.Errorf("Interactive(%v) = %v, want %v", tc.argv, got, tc.long)
		}
	}
}
