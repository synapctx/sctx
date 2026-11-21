package psql

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

func TestAlignedUnderCapIsLosslessCompaction(t *testing.T) {
	raw := fixture(t, "aligned.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	if out.Elided || strings.Contains(body, "…+") {
		t.Fatalf("25 rows must not be elided: %q", body)
	}
	for _, want := range []string{"id|name|status|note", "1|job- 1|running|detail-detail-", "7|job- 7|running|∅", "25|job-25", "(25 rows)"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "----+") {
		t.Errorf("rule line retained: %q", body)
	}
	assertGain(t, raw, body)
}

func TestAlignedAboveCapKeepsEdgesAndCountsRemainder(t *testing.T) {
	raw := fixture(t, "pg17_rows400.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"id|customer_id|status|total|note", "1|2|pending|3.17|order note 1", "150|", "…+200 psql rows", "351|", "400|", "(400 rows)"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "\n151|") || strings.Contains(body, "\n350|") || !out.Elided {
		t.Errorf("middle rows not bounded or elision unmarked")
	}
	assertGain(t, raw, body)
}

func TestExactlyAtCapIsNotElided(t *testing.T) {
	var b strings.Builder
	b.WriteString(" n \n---\n")
	for i := 1; i <= rowCap; i++ {
		fmt.Fprintf(&b, "%2d \n", i)
	}
	fmt.Fprintf(&b, "(%d rows)\n", rowCap)
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(b.String())})
	if err != nil {
		t.Fatal(err)
	}
	if out.Elided || strings.Contains(string(out.Body), "…+") {
		t.Fatal("rows at the cap must all be kept")
	}
}

func TestNativeExpandedRecordsAreCompactedWhole(t *testing.T) {
	raw := fixture(t, "expanded.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{
		Stdout: strings.NewReader(raw), Stderr: strings.NewReader("NOTICE: retained by the run service\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"-[ RECORD 1 ]\n", "-[ RECORD 6 ]", "-[ RECORD 12 ]"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if out.Elided || strings.Contains(body, "…+") {
		t.Errorf("12 records must not be elided: %q", body)
	}
	if out.FoldStderr {
		t.Error("psql stderr notices must be re-emitted verbatim")
	}
	assertGain(t, raw, body)
}

func TestExpandedAboveCapCountsRecords(t *testing.T) {
	raw := fixture(t, "pg17_records120.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"-[ RECORD 70 ]", "…+20 psql records", "-[ RECORD 91 ]", "-[ RECORD 120 ]", "email|user120@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "-[ RECORD 71 ]") || !out.Elided {
		t.Error("middle records not bounded")
	}
	assertGain(t, raw, body)
}

func TestTuplesAndCSVBoundedByLinesOnlyWhenArgvSelectsThem(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		argv       []string
		want       bool
		marker     string
	}{
		{"tuples -tA", "pg17_tuples300.stdout", []string{"psql", "-tA", "-c", "q"}, true, "…+100 psql lines"},
		{"tuples split flags", "pg17_tuples300.stdout", []string{"psql", "-t", "-A"}, true, "…+100 psql lines"},
		{"csv", "pg17_csv300.stdout", []string{"psql", "--csv", "-c", "q"}, true, "…+101 psql lines"},
		{"unknown mode declines", "pg17_tuples300.stdout", []string{"psql", "-c", "q"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := New().Aggressive(context.Background(), format.Input{Argv: tc.argv, Stdout: strings.NewReader(raw)})
			if !tc.want {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out.Body), tc.marker) || !out.Elided {
				t.Errorf("marker missing: %q", out.Body)
			}
		})
	}
}

func TestTimingFooterKept(t *testing.T) {
	raw := fixture(t, "pg17_timing.stdout")
	out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(strings.TrimPrefix(raw, "Timing is on.\n"))})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "(12 rows)\nTime: 1.245 ms") {
		t.Errorf("%q", out.Body)
	}
}

func TestFailureNeverCompacted(t *testing.T) {
	in := format.Input{Stdout: strings.NewReader(fixture(t, "pg17_rows400.stdout")), Stderr: strings.NewReader(fixture(t, "pg17_syntax.stderr")), ExitCode: 1}
	if _, err := New().Aggressive(context.Background(), in); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsafeOrUnrecognizedShapesStayVerbatim(t *testing.T) {
	cases := []format.Input{
		{Stdout: strings.NewReader("CREATE TABLE\nINSERT 0 25\n")},
		{Stdout: strings.NewReader("id,name\n1,alice\n2,bob\n")},
		{Stdout: strings.NewReader(fixture(t, "aligned.stdout")), ExitCode: 1},
		{Stdout: strings.NewReader(strings.Replace(fixture(t, "aligned.stdout"), "(25 rows)", "(24 rows)", 1))},
		{Stdout: strings.NewReader(strings.Replace(fixture(t, "aligned.stdout"), "  8 | job- 8", "wrapped continuation", 1))},
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
