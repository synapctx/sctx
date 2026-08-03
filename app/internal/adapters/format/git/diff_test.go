package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const diffWithContext = `diff --git a/e.txt b/e.txt
index b3c5a95..cf92929 100644
--- a/e.txt
+++ b/e.txt
@@ -1,5 +1,5 @@
 line1
 line2
-line3
+CHANGED
 line4
 line5
`

const showFixture = `commit 850a31286280cca0ad505602acf6a4f3469153c2
Author: Test User <test@example.com>
Date:   Mon Jul 6 22:29:28 2026 +0100

    add e.txt

diff --git a/e.txt b/e.txt
new file mode 100644
index 0000000..b3c5a95
--- /dev/null
+++ b/e.txt
@@ -0,0 +1,5 @@
+line1
+line2
+line3
+line4
+line5
`

func TestAggressiveDiff(t *testing.T) {
	f := New()

	t.Run("drops unchanged context, keeps headers and changed lines", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "diff"},
			Stdout: strings.NewReader(diffWithContext),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{"diff --git a/e.txt b/e.txt", "--- a/e.txt", "+++ b/e.txt", "@@ -1,5 +1,5 @@", "-line3", "+CHANGED", "1 files changed"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		for _, unwanted := range []string{"\n line1\n", "\n line2\n", "\n line4\n", "\n line5\n"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("body still contains unchanged context %q: %q", unwanted, body)
			}
		}
		if len(out.Body) >= len(diffWithContext) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(diffWithContext))
		}
	})

	t.Run("show keeps commit header and diff, drops added-line noise only", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "show"},
			Stdout: strings.NewReader(showFixture),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{"commit 850a312", "add e.txt", "diff --git a/e.txt b/e.txt", "+line1", "1 files changed"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
	})

	t.Run("empty stdout is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"git", "diff"}, Stdout: strings.NewReader("")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("--stat caps the per-file list and always keeps the summary", func(t *testing.T) {
		var lines []string
		for i := 0; i < 40; i++ {
			lines = append(lines, fmt.Sprintf(" file%02d.go | 3 ++-", i))
		}
		lines = append(lines, "40 files changed, 80 insertions(+), 40 deletions(-)")
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"git", "diff", "--stat"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "40 files changed, 80 insertions(+), 40 deletions(-)") {
			t.Errorf("body missing summary line: %q", body)
		}
		if !strings.Contains(body, "…+10 more files") {
			t.Errorf("body missing elision marker: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("--cached uses the same unified-diff parsing as plain diff", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "diff", "--cached"},
			Stdout: strings.NewReader(diffWithContext),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "-line3") || !strings.Contains(string(out.Body), "+CHANGED") {
			t.Errorf("body missing changed lines: %q", out.Body)
		}
	})
}
