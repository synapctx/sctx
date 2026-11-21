package filediff

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/difffloor"
)

// plain.diff is `diff -u -U40 a.txt b.txt` captured from the real binary.
func TestFidelityFloorAndStamp(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "plain.diff"))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	n := strings.Count(raw, "\n")
	in := func() format.Input {
		return format.Input{Argv: []string{"diff", "-u"}, Stdout: strings.NewReader(raw), ExitCode: 1}
	}

	t.Run("at the floor passes through, both tiers", func(t *testing.T) {
		t.Setenv(difffloor.EnvVar, strconv.Itoa(n))
		if _, err := New().Aggressive(context.Background(), in()); err != format.ErrTierInapplicable {
			t.Fatalf("aggressive err = %v", err)
		}
		if _, err := New().Relaxed(context.Background(), in()); err != format.ErrTierInapplicable {
			t.Fatalf("relaxed err = %v", err)
		}
	})

	t.Run("above the floor compacts with a stamp first", func(t *testing.T) {
		t.Setenv(difffloor.EnvVar, strconv.Itoa(n-1))
		out, err := New().Aggressive(context.Background(), in())
		if err != nil {
			t.Fatal(err)
		}
		body := string(out.Body)
		want := difffloor.Stamp(n, strings.Count(body, "\n")+1)
		if got := strings.SplitN(body, "\n", 2)[0]; got != want {
			t.Errorf("stamp = %q, want %q", got, want)
		}
		if len(body) >= len(raw) {
			t.Errorf("not smaller: %d >= %d", len(body), len(raw))
		}
	})

	t.Run("non-UTF-8 declines", func(t *testing.T) {
		t.Setenv(difffloor.EnvVar, "0")
		bad := strings.Replace(raw, "FIFTY", "FIFT\xe9", 1)
		_, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"diff", "-u"}, Stdout: strings.NewReader(bad), ExitCode: 1})
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v", err)
		}
	})
}
