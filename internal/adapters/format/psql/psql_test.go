package psql

import (
	"context"
	"errors"
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

func TestNativeAlignedTableKeepsShapeAndEdgeRows(t *testing.T) {
	raw := fixture(t, "aligned.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{" id |  name  | status", "  1 | job- 1", "  5 | job- 5", "…+18 psql rows", " 24 | job-24", " 25 | job-25", "(25 rows)"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "  6 | job- 6") || !out.Elided {
		t.Errorf("middle rows not bounded or elision unmarked: %q", body)
	}
	assertGain(t, raw, body)
}

func TestNativeExpandedRecordsStayWhole(t *testing.T) {
	raw := fixture(t, "expanded.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{
		Stdout: strings.NewReader(raw), Stderr: strings.NewReader("NOTICE: retained by the run service\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"-[ RECORD 1 ]", "-[ RECORD 5 ]", "…+5 psql records", "-[ RECORD 11 ]", "-[ RECORD 12 ]"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "-[ RECORD 6 ]") {
		t.Errorf("middle record retained: %q", body)
	}
	if out.FoldStderr {
		t.Error("psql stderr notices must be re-emitted verbatim")
	}
	assertGain(t, raw, body)
}

func TestUnsafeOrUnrecognizedShapesStayVerbatim(t *testing.T) {
	cases := []format.Input{
		{Stdout: strings.NewReader("CREATE TABLE\nINSERT 0 25\n")},
		{Stdout: strings.NewReader("id,name\n1,alice\n2,bob\n")},
		{Stdout: strings.NewReader(fixture(t, "aligned.stdout")), ExitCode: 1},
		{Stdout: strings.NewReader(strings.Replace(fixture(t, "aligned.stdout"), "(25 rows)", "(24 rows)", 1))},
		{Stdout: strings.NewReader(strings.Replace(fixture(t, "aligned.stdout"), "  8 | job- 8", "wrapped continuation", 1))},
		{Stdout: strings.NewReader("n\n-\n1\n2\n3\n4\n5\n6\n7\n8\n(8 rows)\n")},
	}
	for i, in := range cases {
		if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("case %d error = %v", i, err)
		}
	}
}

func assertGain(t *testing.T, raw, body string) {
	t.Helper()
	rawTokens := tokenizer.Estimate(int64(len(raw)))
	outTokens := tokenizer.Estimate(int64(len(body)))
	if outTokens >= rawTokens {
		t.Fatalf("estimated tokens did not decrease: %d >= %d", outTokens, rawTokens)
	}
	t.Logf("native psql: %d -> %d estimated tokens (%.1f%% saved)", rawTokens, outTokens,
		100*float64(rawTokens-outTokens)/float64(rawTokens))
}
