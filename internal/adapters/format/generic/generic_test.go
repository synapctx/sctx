package generic

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/tokenizer"
)

func render(t *testing.T, stdout string) (body string, tier string) {
	return renderInput(t, stdout, 0)
}

func renderInput(t *testing.T, stdout string, exitCode int) (body string, tier string) {
	t.Helper()
	f := New()
	in := func() format.Input {
		return format.Input{Stdout: strings.NewReader(stdout), ExitCode: exitCode}
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
	for range 40 {
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
	for range 20 {
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

func TestJSONLinesFromAnUncoveredCommandAreBounded(t *testing.T) {
	var records []string
	for i := range 12 {
		records = append(records, fmt.Sprintf(`{ "seq": %d, "message": "event" }`, i))
	}
	raw := strings.Join(records, "\r\n") + "\r\n"
	body, tier := render(t, raw)
	if tier != "aggressive" {
		t.Fatalf("tier = %s, want aggressive", tier)
	}
	if !strings.Contains(body, "…+5 more JSON records") {
		t.Fatalf("body lacks exact record marker: %q", body)
	}
	// The head and the TAIL survive: the end of an NDJSON stream is where a log
	// puts its failure and its summary.
	if !strings.HasPrefix(body, `{"seq":0,`) || !strings.HasSuffix(body, `{"seq":11,"message":"event"}`) {
		t.Errorf("the bounded stream lost an end: %q", body)
	}
	if strings.Contains(body, `{ "seq"`) {
		t.Errorf("kept JSON records were not compacted: %q", body)
	}
}

func TestMixedJSONLinesRemainVerbatimEvenWhenInvalidLinesRepeat(t *testing.T) {
	raw := "{\"n\":1}\nnot-json\nnot-json\nnot-json\n{\"n\":5}\n{\"n\":6}\n{\"n\":7}\n{\"n\":8}\n"
	body, tier := render(t, raw)
	if tier != "verbatim" || body != raw {
		t.Fatalf("mixed stream changed: tier=%s\n got %q\nwant %q", tier, body, raw)
	}
}

func TestFailedJSONLinesCommandRemainsVerbatim(t *testing.T) {
	var records []string
	for i := range 12 {
		records = append(records, fmt.Sprintf(`{"error":"failure %d"}`, i))
	}
	raw := strings.Join(records, "\n") + "\n"
	body, tier := renderInput(t, raw, 1)
	if tier != "verbatim" || body != raw {
		t.Fatalf("failed stream changed: tier=%s\n got %q\nwant %q", tier, body, raw)
	}
}

// This uses the installed binaries instead of an imagined fixture. jq emits
// exactly the compact record stream requested by the caller; curl then returns
// those bytes from a local file URL. The denominator is therefore each native
// command's real stdout, with no verbosity added by sctx.
func TestNativeJSONLinesMeasurements(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	filter := `range(0;100) | {seq:.,level:"info",message:"resource ready",resource:("pod-"+(.|tostring))}`
	raw, err := exec.Command("jq", "-nc", filter).Output()
	if err != nil {
		t.Fatalf("native jq JSONL command: %v", err)
	}
	assertMeasuredJSONLinesGain(t, "jq", raw)

	t.Run("curl local NDJSON response", func(t *testing.T) {
		if _, err := exec.LookPath("curl"); err != nil {
			t.Skip("curl not installed")
		}
		path := filepath.Join(t.TempDir(), "events.ndjson")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		// A plain url.URL{Scheme:"file", Path: path} mishandles a Windows
		// path: backslashes go into the URL unescaped and there is no
		// leading slash before the drive letter, so curl.exe cannot parse
		// it. Build the file:// form curl accepts on every platform.
		slashPath := filepath.ToSlash(path)
		if !strings.HasPrefix(slashPath, "/") {
			slashPath = "/" + slashPath
		}
		resource := "file://" + slashPath
		curlRaw, err := exec.Command("curl", "--silent", "--show-error", resource).Output()
		if err != nil {
			t.Fatalf("native curl local NDJSON command: %v", err)
		}
		assertMeasuredJSONLinesGain(t, "curl", curlRaw)
	})
}

func TestNativeSQLiteGenericCoverage(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	query := `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100) SELECT x AS seq, 'ready' AS status, printf('pod-%03d', x) AS resource FROM n;`
	rawJSON, err := exec.Command("sqlite3", "-json", ":memory:", query).Output()
	if err != nil {
		t.Fatalf("native sqlite3 -json: %v", err)
	}
	body, tier := render(t, string(rawJSON))
	if tier != "aggressive" {
		t.Fatalf("sqlite3 JSON tier = %s", tier)
	}
	rawTokens := tokenizer.Estimate(int64(len(rawJSON)))
	outTokens := tokenizer.Estimate(int64(len(body)))
	if outTokens >= rawTokens {
		t.Fatalf("sqlite3 JSON did not save tokens: %d >= %d", outTokens, rawTokens)
	}
	t.Logf("sqlite3 native JSON: %d -> %d estimated tokens (%.1f%% saved)", rawTokens, outTokens,
		100*float64(rawTokens-outTokens)/float64(rawTokens))

	rawDefault, err := exec.Command("sqlite3", ":memory:", query).Output()
	if err != nil {
		t.Fatalf("native sqlite3 default output: %v", err)
	}
	defaultBody, defaultTier := render(t, string(rawDefault))
	if defaultTier != "verbatim" || defaultBody != string(rawDefault) {
		t.Fatalf("unique schema-less rows changed: tier=%s", defaultTier)
	}
}

func assertMeasuredJSONLinesGain(t *testing.T, command string, raw []byte) {
	t.Helper()
	body, tier := render(t, string(raw))
	if tier != "aggressive" || !strings.Contains(body, "…+93 more JSON records") {
		t.Fatalf("%s native stream: tier=%s body=%q", command, tier, body)
	}
	rawTokens := tokenizer.Estimate(int64(len(raw)))
	outTokens := tokenizer.Estimate(int64(len(body)))
	if outTokens >= rawTokens {
		t.Fatalf("%s native stream did not save tokens: %d >= %d", command, outTokens, rawTokens)
	}
	t.Logf("%s native JSONL: %d -> %d estimated tokens (%d saved, %.1f%%)", command,
		rawTokens, outTokens, rawTokens-outTokens,
		100*float64(rawTokens-outTokens)/float64(rawTokens))
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
