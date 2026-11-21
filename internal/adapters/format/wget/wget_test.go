package wget

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

func run(t *testing.T, argv []string, raw string, exit int) (format.Rendered, error) {
	t.Helper()
	return New().Aggressive(context.Background(), format.Input{Argv: argv, Stderr: strings.NewReader(raw), ExitCode: exit})
}

func TestBigDownloadCollapses(t *testing.T) {
	raw := fixture(t, "big.txt")
	out, err := run(t, []string{"wget", "https://example.com/go.tgz", "-O", "big.tgz"}, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if len(body)*10 >= len(raw) {
		t.Errorf("expected >10x reduction, got %d -> %d", len(raw), len(body))
	}
	for _, k := range []string{
		"HTTP request sent, awaiting response... 302 Found",
		"Location: https://dl.google.com/go/go1.22.0.src.tar.gz [following]",
		"Length: 27544122 (26M) [application/x-gzip]",
		"Saving to: ‘big.tgz’",
		"progress lines",
		"26850K .......... .......... .......... .......... ........  100% 20.0M=2.5s",
		"‘big.tgz’ saved [27544122/27544122]",
	} {
		if !strings.Contains(body, k) {
			t.Errorf("lost %q", k)
		}
	}
	if strings.Contains(body, "   50K ..........") {
		t.Error("kept a mid-download progress row")
	}
	if !out.Elided || !out.FoldStderr {
		t.Errorf("Elided=%v FoldStderr=%v", out.Elided, out.FoldStderr)
	}
}

func TestFailuresAndSmallRunsDecline(t *testing.T) {
	for _, f := range []string{"notfound.txt", "dns.txt", "recursive.txt"} {
		_, err := run(t, []string{"wget", "x"}, fixture(t, f), 8)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestNonZeroExitKeepsErrorLines(t *testing.T) {
	var b strings.Builder
	b.WriteString("HTTP request sent, awaiting response... 200 OK\nLength: 9999999 (9.5M)\nSaving to: ‘f’\n\n")
	b.WriteString("     0K .......... .......... .......... .......... ..........  0%  1M 9s\n")
	b.WriteString("    50K .......... .......... .......... .......... ..........  1%  1M 9s\n")
	b.WriteString("   100K .......... .......... .......... .......... ..........  1%  1M 9s\n")
	b.WriteString("   150K .......... .......... .......... .......... ..........  2%  1M 9s\n")
	b.WriteString("\n2026-11-21 02:25:15 (1 MB/s) - Read error at byte 1234/9999999 (Connection reset by peer). Retrying.\n")
	out, err := run(t, []string{"wget", "x"}, b.String(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "Read error at byte 1234/9999999 (Connection reset by peer). Retrying.") {
		t.Errorf("lost error line:\n%s", out.Body)
	}
}

func TestStdoutPayloadDeclines(t *testing.T) {
	raw := fixture(t, "big.txt")
	for _, argv := range [][]string{{"wget", "-O", "-", "u"}, {"wget", "-O-", "u"}, {"wget", "--output-document=-", "u"}} {
		if _, err := run(t, argv, raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("%v: %v", argv, err)
		}
	}
}
