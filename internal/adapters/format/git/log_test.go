package git

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/format"
)

func gitLogFixture(t *testing.T) string {
	t.Helper()
	mk := func(hash, subject string, when time.Time) string {
		return fmt.Sprintf("commit %s\nAuthor: Test User <test@example.com>\nDate:   %s\n\n    %s\n\n",
			hash, when.Format(gitDateLayout), subject)
	}
	now := time.Now()
	return mk("850a31286280cca0ad505602acf6a4f3469153c2", "add e.txt", now.Add(-time.Hour)) +
		mk("554ba768e55083456f367f08401ea319723cc306", "third commit", now.Add(-24*time.Hour)) +
		mk("debf6884ac41d2c364e2cdf15a47abf0617c938d", "second commit", now.Add(-48*time.Hour)) +
		mk("d018a401e247693dcd3a36e7733e2dace9576b95", "initial commit", now.Add(-72*time.Hour))
}

func TestAggressiveLog(t *testing.T) {
	f := New()

	t.Run("default format collapses to one line per commit", func(t *testing.T) {
		raw := gitLogFixture(t)
		in := format.Input{
			Argv:   []string{"git", "log"},
			Stdout: strings.NewReader(raw),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		lines := strings.Split(body, "\n")
		if len(lines) != 4 {
			t.Fatalf("got %d lines, want 4: %q", len(lines), body)
		}
		for _, want := range []string{"850a312 add e.txt", "554ba76 third commit", "debf688 second commit", "d018a40 initial commit"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if !strings.Contains(body, "Test User") || !strings.Contains(body, "ago") {
			t.Errorf("body missing author/relative-date: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("--oneline is already compact, tier inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "log", "--oneline"},
			Stdout: strings.NewReader("850a312 add e.txt\n"),
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("--format is already compact, tier inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "log", "--format=%h %s"},
			Stdout: strings.NewReader("850a312 add e.txt\n"),
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("--oneline over the cap keeps the most recent commits and elides the rest", func(t *testing.T) {
		var lines []string
		for i := range 50 {
			lines = append(lines, fmt.Sprintf("%07x commit number %d", i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{
			Argv:   []string{"git", "log", "--oneline"},
			Stdout: strings.NewReader(raw),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "commit number 0") {
			t.Errorf("body missing most recent commit: %q", body)
		}
		if !strings.Contains(body, "…+20 more commits") {
			t.Errorf("body missing elision marker: %q", body)
		}
		if strings.Contains(body, "commit number 49") {
			t.Errorf("body should not contain elided commit: %q", body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("--oneline under the cap is inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "log", "--oneline"},
			Stdout: strings.NewReader("850a312 add e.txt\n554ba76 third commit\n"),
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
