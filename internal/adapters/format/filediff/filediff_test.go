package filediff

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// unifiedFixture is real `diff -U 10` output between two 30-line files
// differing on one line, giving two 10-line context runs long enough for
// collapsing to be a genuine size win.
const unifiedFixture = `--- a2.txt	2026-07-07 07:48:39.181719148 +0100
+++ b2.txt	2026-07-07 07:48:39.181867816 +0100
@@ -5,21 +5,21 @@
 line5
 line6
 line7
 line8
 line9
 line10
 line11
 line12
 line13
 line14
-line15
+CHANGED15
 line16
 line17
 line18
 line19
 line20
 line21
 line22
 line23
 line24
 line25
`

const edStyleFixture = `2c2
< y
---
> Y
`

func TestAggressive(t *testing.T) {
	f := New()

	t.Run("collapses long context runs, keeps headers and changed lines", func(t *testing.T) {
		in := format.Input{Argv: []string{"diff", "-U10", "a2.txt", "b2.txt"}, Stdout: strings.NewReader(unifiedFixture), ExitCode: 1}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "1 hunks, +1 -1") {
			t.Errorf("body missing summary, got: %q", body[:min(30, len(body))])
		}
		for _, want := range []string{"--- a2.txt", "+++ b2.txt", "@@ -5,21 +5,21 @@", "-line15", "+CHANGED15", " line5", " line25"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if !strings.Contains(body, "…+8 unchanged") {
			t.Errorf("body missing collapse marker, got: %q", body)
		}
		if strings.Contains(body, " line7\n") || strings.Contains(body, " line20\n") {
			t.Errorf("body still contains collapsed middle context lines: %q", body)
		}
		if len(out.Body) >= len(unifiedFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(unifiedFixture))
		}
	})

	t.Run("ed-style output is already minimal, inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"diff", "c.txt", "d.txt"}, Stdout: strings.NewReader(edStyleFixture), ExitCode: 1}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("exit 1 (files differ) still formats, not treated as error", func(t *testing.T) {
		in := format.Input{Argv: []string{"diff", "-U10", "a2.txt", "b2.txt"}, Stdout: strings.NewReader(unifiedFixture), ExitCode: 1}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v, want a formatted result despite exit 1", err)
		}
		if len(out.Body) == 0 {
			t.Errorf("expected non-empty body for exit 1 unified diff")
		}
	})

	t.Run("exit 2 (real error) degrades", func(t *testing.T) {
		in := format.Input{
			Argv:     []string{"diff", "a.txt", "/nonexistent"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader("diff: /nonexistent: No such file or directory\n"),
			ExitCode: 2,
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("empty stdout is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"diff", "a.txt", "a.txt"}, Stdout: strings.NewReader(""), ExitCode: 0}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
