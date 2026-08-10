package generic

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func render(t *testing.T, stdout string) (body string, tier string) {
	t.Helper()
	f := New()
	in := func() format.Input {
		return format.Input{Stdout: strings.NewReader(stdout)}
	}
	if r, err := f.Aggressive(context.Background(), in()); err == nil {
		return string(r.Body), "aggressive"
	} else if err != format.ErrTierInapplicable {
		t.Fatalf("aggressive: %v", err)
	}
	if r, err := f.Relaxed(context.Background(), in()); err == nil {
		return string(r.Body), "relaxed"
	} else if err != format.ErrTierInapplicable {
		t.Fatalf("relaxed: %v", err)
	}
	return stdout, "verbatim"
}

// THE CASE THAT WAS WORTH ZERO. Repetitive text from a command with no dedicated
// formatter: measured across 179 runs and 50,124 raw tokens, the old fallback
// saved nothing at all because it only looked at output starting with `{`.
func TestRepetitiveTextFromAnUncoveredCommandIsCollapsed(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("Waiting for resource to become ready...\n")
	}
	body, tier := render(t, b.String())

	if tier != "relaxed" {
		t.Fatalf("tier = %s, want relaxed: 40 identical lines must not reach verbatim", tier)
	}
	if !strings.Contains(body, "×40") {
		t.Errorf("collapsed output does not state how many lines it stands for: %q", body)
	}
	if len(body) >= len(b.String()) {
		t.Errorf("render was not smaller (%d >= %d)", len(body), len(b.String()))
	}
}

// The Windows failure that produces no error and no anomaly — just silently zero
// savings, on the platform least likely to be tested.
func TestCRLFOutputStillCollapses(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("Copying file to destination\r\n")
	}
	body, tier := render(t, b.String())

	if tier != "relaxed" {
		t.Fatalf("tier = %s, want relaxed: a trailing \\r must not defeat duplicate detection", tier)
	}
	if !strings.Contains(body, "×20") {
		t.Errorf("CRLF run was not collapsed: %q", body)
	}
}

// JSON must keep its existing behaviour: this formatter absorbed the old
// content-sniffer, and a regression there would be a silent loss on every
// `curl`, `jq`, and now every cloud CLI defaulting to JSON.
func TestJSONStillCompacts(t *testing.T) {
	body, tier := render(t, "{\n  \"a\": 1,\n  \"b\": [1, 2, 3],\n  \"c\": \"x\"\n}\n")
	if tier != "aggressive" {
		t.Fatalf("tier = %s, want aggressive for a JSON document", tier)
	}
	if strings.Contains(body, "\n  ") {
		t.Errorf("JSON was not compacted: %q", body)
	}
}

// The property that makes it safe to point this at commands nobody has captured
// a fixture for: with nothing provably redundant, it declines rather than
// inventing a saving.
func TestUniqueTextIsLeftAlone(t *testing.T) {
	in := "alpha\nbravo\ncharlie\ndelta\necho\n"
	body, tier := render(t, in)
	if tier != "verbatim" {
		t.Errorf("tier = %s, want verbatim: nothing here is redundant", tier)
	}
	if body != in {
		t.Errorf("output changed despite having no redundancy:\n got %q\nwant %q", body, in)
	}
}

// Two identical lines are not a run. The threshold exists so that ordinary
// output — a pair of matching log lines — is never reported as compressed.
func TestARunBelowThresholdIsNotCollapsed(t *testing.T) {
	if _, tier := render(t, "same\nsame\nother\n"); tier != "verbatim" {
		t.Errorf("tier = %s, want verbatim for a 2-line run", tier)
	}
}

// The label must stay distinguishable from a dedicated formatter, or the
// coverage-gap analysis cannot tell a covered command from a caught one.
func TestDescriptorIsNotMistakenForCoverage(t *testing.T) {
	if got := New().Descriptor().Command; got != "(generic)" {
		t.Errorf("Descriptor().Command = %q, want %q", got, "(generic)")
	}
}
