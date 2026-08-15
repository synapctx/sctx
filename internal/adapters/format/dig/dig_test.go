package dig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/tokenizer"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNativeFullResponsesKeepDNSMeaning(t *testing.T) {
	tests := []struct {
		name  string
		wants []string
		drops int
	}{
		{"example-com.stdout", []string{"status: NOERROR", ";; flags:", ";; QUESTION SECTION:", ";; ANSWER SECTION:", "104.20.23.154", "172.66.147.243", ";; Query time:", ";; SERVER:"}, 7},
		{"nxdomain.stdout", []string{"status: NXDOMAIN", "ANSWER: 0", ";; AUTHORITY SECTION:", "SOA", ";; Query time:", ";; SERVER:"}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fixture(t, tt.name)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"dig", "example.com", "A"}, Stdout: strings.NewReader(raw)})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			for _, want := range tt.wants {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q in %q", want, body)
				}
			}
			if !strings.Contains(body, fmt.Sprintf("…+%d dig metadata lines", tt.drops)) {
				t.Errorf("missing exact metadata count in %q", body)
			}
			for _, omitted := range []string{"<<>> DiG", ";; WHEN:", ";; MSG SIZE"} {
				if strings.Contains(body, omitted) {
					t.Errorf("metadata %q retained", omitted)
				}
			}
			if !out.Elided || len(out.Body) >= len(raw) {
				t.Errorf("render elided=%t size=%d raw=%d", out.Elided, len(out.Body), len(raw))
			}
			rawTokens := tokenizer.Estimate(int64(len(raw)))
			outTokens := tokenizer.Estimate(int64(len(out.Body)))
			if outTokens >= rawTokens {
				t.Errorf("estimated tokens did not decrease: %d >= %d", outTokens, rawTokens)
			}
			t.Logf("native dig %s: %d -> %d estimated tokens (%.1f%% saved)", tt.name,
				rawTokens, outTokens, 100*float64(rawTokens-outTokens)/float64(rawTokens))
		})
	}
}

func TestMinimalAndFailedOutputStayVerbatim(t *testing.T) {
	for _, in := range []format.Input{
		{Argv: []string{"dig", "+short", "example.com"}, Stdout: strings.NewReader("104.20.23.154\n172.66.147.243\n")},
		{Argv: []string{"dig", "example.com"}, Stderr: strings.NewReader("network unreachable\n"), ExitCode: 9},
		{Argv: []string{"dig", "example.com"}, Stdout: strings.NewReader(fixture(t, "example-com.stdout")), Stderr: strings.NewReader("unexpected warning\n")},
	} {
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestUnknownLinesArePreserved(t *testing.T) {
	raw := strings.Replace(fixture(t, "example-com.stdout"), ";; ANSWER SECTION:", ";; ANSWER SECTION:\ncritical future diagnostic", 1)
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(raw)})
	if err != nil || !strings.Contains(string(out.Body), "critical future diagnostic") {
		t.Fatalf("unknown line lost: %q, %v", out.Body, err)
	}
}
