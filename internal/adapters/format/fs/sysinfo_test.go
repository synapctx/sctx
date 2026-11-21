package fs

import (
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDfFormatter(t *testing.T) {
	b, err := os.ReadFile("testdata/df-h.macos.txt") // captured from real macOS df -h
	if err != nil {
		t.Fatal(err)
	}
	out, err := (&dfFormatter{}).Aggressive(testCtx, format.Input{Argv: []string{"df", "-h"}, Stdout: strings.NewReader(string(b))})
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{
		"Filesystem Size Used Avail Capacity Mounted-on",
		"/dev/disk3s1s1 926Gi 13Gi 157Gi 8% /",
		"/dev/disk3s5 926Gi 746Gi 157Gi 83% /System/Volumes/Data",
		"/dev/disk3s3 926Gi 1.4Gi 157Gi 1% /Volumes/Recovery",
		"system/pseudo mounts folded",
		"/dev", "/System/Volumes/VM", "/System/Volumes/xarts", "/System/Volumes/Data/home",
		"(inode columns omitted",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if len(body) >= len(b) {
		t.Errorf("not smaller: %d >= %d", len(body), len(b))
	}

	t.Run("df -i keeps inode columns", func(t *testing.T) {
		out, err := (&dfFormatter{}).Aggressive(testCtx, format.Input{Argv: []string{"df", "-i"}, Stdout: strings.NewReader(string(b))})
		if err == nil && strings.Contains(string(out.Body), "inode columns omitted") {
			t.Errorf("dropped inode columns despite -i: %s", out.Body)
		}
	})
}

func TestEnvFormatter(t *testing.T) {
	long := "/opt/a:/opt/b:/opt/c:/opt/d:/opt/e:/opt/f:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/Library/Apple/usr/bin:/opt/extra/bin:/opt/more/bin"
	var sb strings.Builder
	for _, l := range []string{"HOME=/home/x", "SHELL=/bin/zsh", "LANG=en_US.UTF-8", "TERM=xterm", "USER=x", "EDITOR=vim", "PAGER=less", "SHLVL=1"} {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("PATH=" + long + "\n")
	sb.WriteString("LS_COLORS=" + strings.Repeat("di=01;34:", 30) + "\n")
	secret := "API_TOKEN=" + strings.Repeat("abcdef0123456789", 10)
	sb.WriteString(secret + "\n")
	raw := sb.String()

	out, err := (&envFormatter{command: "env"}).Aggressive(testCtx, format.Input{Argv: []string{"env"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "PATH=/opt/a:/opt/b:/opt/c:/opt/d:…+10 entries (") {
		t.Errorf("PATH not collapsed with a count: %s", body)
	}
	if !strings.Contains(body, "LS_COLORS=") || !strings.Contains(body, "…+") {
		t.Errorf("long value not shortened with a marker: %s", body)
	}
	if !strings.Contains(body, secret) {
		t.Errorf("secret-named value must pass through byte-for-byte for the redaction layer: %s", body)
	}
	for _, name := range []string{"HOME", "SHELL", "LANG", "TERM", "USER", "EDITOR", "PAGER", "SHLVL", "PATH", "LS_COLORS", "API_TOKEN"} {
		if !strings.Contains(body, name+"=") {
			t.Errorf("variable %s lost", name)
		}
	}
	if _, err := (&envFormatter{command: "env"}).Aggressive(testCtx, format.Input{Argv: []string{"env", "-0"}, Stdout: strings.NewReader(raw)}); err != format.ErrTierInapplicable {
		t.Errorf("env -0 must decline, got %v", err)
	}
}

func TestWcFormatter(t *testing.T) {
	raw := "      10      42     300 a.go\n     101     420    3000 dir/b c.go\n       1       2       3 d\n       5       5       5 e\n       9       9       9 f\n     126     478    3317 total\n"
	out, err := (&wcFormatter{}).Aggressive(testCtx, stdoutInput("wc", raw))
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "101 420 3000 dir/b c.go") || !strings.HasSuffix(body, "126 478 3317 total") {
		t.Errorf("counts or names damaged: %q", body)
	}
}

func TestWcFactorsSharedDirectory(t *testing.T) {
	raw := "  1  2  3 internal/adapters/a.go\n  4  5  6 internal/adapters/x/b.go\n  7  8  9 internal/adapters/c.go\n  1  1  1 internal/adapters/d.go\n  2  2  2 internal/adapters/e.go\n  15  18  21 total\n"
	out, err := (&wcFormatter{}).Aggressive(testCtx, stdoutInput("wc", raw))
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "(paths relative to internal/adapters/)\n1 2 3 a.go\n4 5 6 x/b.go\n") || !strings.HasSuffix(body, "15 18 21 total") {
		t.Errorf("unexpected render: %q", body)
	}
}

func TestLsofFormatter(t *testing.T) {
	raw := "COMMAND     PID USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME\n" +
		"rapportd    512 me      9u  IPv4 0x8f2a1c3d4e5f6071      0t0  TCP *:49152 (LISTEN)\n" +
		"rapportd    512 me     10u  IPv6 0x8f2a1c3d4e5f6072      0t0  TCP *:49152 (LISTEN)\n" +
		"node      20113 me     21u  IPv4 0x8f2a1c3d4e5f6073      0t0  TCP 127.0.0.1:3000->127.0.0.1:55001 (ESTABLISHED)\n" +
		"node      20113 me     22u  IPv4 0x8f2a1c3d4e5f6074      0t0  TCP 127.0.0.1:3000 (LISTEN)\n" +
		"Google    777 me      99u  IPv4 0x8f2a1c3d4e5f6075      0t0  UDP *:5353\n"
	out, err := (&lsofFormatter{}).Aggressive(testCtx, format.Input{Argv: []string{"lsof", "-i", "-P", "-n"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Aggressive: %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "node 20113 me 21u IPv4 0x8f2a1c3d4e5f6073 0t0 TCP 127.0.0.1:3000->127.0.0.1:55001 (ESTABLISHED)") {
		t.Errorf("row damaged: %q", body)
	}
	if _, err := (&lsofFormatter{}).Aggressive(testCtx, format.Input{Argv: []string{"lsof", "-F", "pn"}, Stdout: strings.NewReader(raw)}); err != format.ErrTierInapplicable {
		t.Errorf("-F must decline, got %v", err)
	}
}
