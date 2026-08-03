package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveBlame(t *testing.T) {
	f := New()

	t.Run("collapses consecutive lines sharing the same commit", func(t *testing.T) {
		var lines []string
		// Lines 1-5: same commit (d018a40).
		for i := 1; i <= 5; i++ {
			lines = append(lines, fmt.Sprintf("d018a401e247 (Alice 2026-01-01 10:00:00 +0000 %2d) line %d", i, i))
		}
		// Line 6: a different, isolated commit (850a312).
		lines = append(lines, "850a31286280 (Bob   2026-07-06 22:29:28 +0100  6) CHANGED line")
		// Lines 7-9: back to the first commit.
		for i := 7; i <= 9; i++ {
			lines = append(lines, fmt.Sprintf("d018a401e247 (Alice 2026-01-01 10:00:00 +0000 %2d) line %d", i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"

		in := format.Input{Argv: []string{"git", "blame", "file.go"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		outLines := strings.Split(body, "\n")
		if len(outLines) != 3 {
			t.Fatalf("got %d collapsed regions, want 3: %q", len(outLines), body)
		}
		if !strings.Contains(outLines[0], "d018a40") || !strings.Contains(outLines[0], "Alice") ||
			!strings.Contains(outLines[0], "L1-5") || !strings.Contains(outLines[0], "×5 lines") {
			t.Errorf("region 1 wrong: %q", outLines[0])
		}
		if !strings.Contains(outLines[1], "850a312") || !strings.Contains(outLines[1], "Bob") ||
			!strings.Contains(outLines[1], "L6") || !strings.Contains(outLines[1], "CHANGED line") {
			t.Errorf("region 2 wrong: %q", outLines[1])
		}
		if !strings.Contains(outLines[2], "d018a40") || !strings.Contains(outLines[2], "L7-9") ||
			!strings.Contains(outLines[2], "×3 lines") {
			t.Errorf("region 3 wrong: %q", outLines[2])
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("unrecognized shape is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"git", "blame", "--porcelain", "file.go"}, Stdout: strings.NewReader("not a blame line\nanother\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
