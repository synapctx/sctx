package git

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

// fixture loads output captured from the real git binary (darwin/arm64) by
// redirecting to files, so no hook or wrapper touched the bytes.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "diff", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, argv []string, raw string) (format.Rendered, error) {
	t.Helper()
	return New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw)})
}

func TestDiffFidelityFloor(t *testing.T) {
	small := fixture(t, "small.diff")
	big := fixture(t, "big.diff")
	bigLines := strings.Count(big, "\n")

	tests := []struct {
		name    string
		floor   string
		raw     string
		decline bool
	}{
		{"small diff under default floor passes through", "200", small, true},
		{"large diff above default floor compacts", "200", big, false},
		{"diff exactly at the floor passes through", strconv.Itoa(bigLines), big, true},
		{"one line above the floor compacts", strconv.Itoa(bigLines - 1), big, false},
		{"floor 0 disables it", "0", small, false},
		{"invalid value falls back to default 200", "abc", small, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(difffloor.EnvVar, tt.floor)
			out, err := run(t, []string{"git", "diff"}, tt.raw)
			if tt.decline {
				if err != format.ErrTierInapplicable {
					t.Fatalf("err = %v, want ErrTierInapplicable", err)
				}
				// The relaxed tier must not rewrite it either.
				if _, err := New().Relaxed(context.Background(), format.Input{Argv: []string{"git", "diff"}, Stdout: strings.NewReader(tt.raw)}); err != format.ErrTierInapplicable {
					t.Fatalf("relaxed err = %v, want ErrTierInapplicable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			first := strings.SplitN(string(out.Body), "\n", 2)[0]
			if !strings.HasPrefix(first, "sctx: compacted diff ") || !strings.HasSuffix(first, " lines, context trimmed") {
				t.Errorf("first line is not the loss stamp: %q", first)
			}
		})
	}
}

func TestDiffStampCountsAreTrue(t *testing.T) {
	t.Setenv(difffloor.EnvVar, "200")
	for _, name := range []string{"big.diff", "show.diff"} {
		raw := fixture(t, name)
		out, err := run(t, []string{"git", "diff"}, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := string(out.Body)
		want := difffloor.Stamp(strings.Count(raw, "\n"), strings.Count(body, "\n")+1)
		if strings.SplitN(body, "\n", 2)[0] != want {
			t.Errorf("%s: stamp = %q, want %q", name, strings.SplitN(body, "\n", 2)[0], want)
		}
		if len(body) >= len(raw) {
			t.Errorf("%s: %d >= %d bytes", name, len(body), len(raw))
		}
		// Every changed line survives.
		for _, l := range []string{"-50", "+FIFTY", "-200", "+TWOH", "-350", "+X"} {
			if !strings.Contains(body, "\n"+l+"\n") {
				t.Errorf("%s: lost changed line %q", name, l)
			}
		}
	}
}

func TestDiffMachineAndOpaqueOutputPassThrough(t *testing.T) {
	t.Setenv(difffloor.EnvVar, "0") // floor off: only the shape guards may decline
	tests := []struct {
		name string
		argv []string
		file string
	}{
		{"show rev:path blob", []string{"git", "show", "HEAD:big.txt"}, "blob.out"},
		{"numstat", []string{"git", "diff", "--numstat", "HEAD~1", "HEAD"}, "numstat.out"},
		{"name-only", []string{"git", "diff", "--name-only", "HEAD~1", "HEAD"}, "nameonly.out"},
		{"name-only -z", []string{"git", "diff", "-z", "--name-only", "HEAD~1", "HEAD"}, "z.out"},
		{"raw", []string{"git", "diff", "--raw", "HEAD~1", "HEAD"}, "raw.out"},
		{"binary patch", []string{"git", "diff", "--binary", "HEAD~1", "HEAD"}, "binary.diff"},
		{"non-UTF-8 diff", []string{"git", "diff", "HEAD~1", "HEAD"}, "latin.diff"},
		{"porcelain-style status flag", []string{"git", "diff", "--porcelain"}, "small.diff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fixture(t, tt.file)
			if _, err := run(t, tt.argv, raw); err != format.ErrTierInapplicable {
				t.Fatalf("aggressive err = %v, want ErrTierInapplicable", err)
			}
			if _, err := New().Relaxed(context.Background(), format.Input{Argv: tt.argv, Stdout: strings.NewReader(raw)}); err != format.ErrTierInapplicable {
				t.Fatalf("relaxed err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

func TestDiffNonUTF8StaysByteExactEvenWhenLarge(t *testing.T) {
	t.Setenv(difffloor.EnvVar, "0")
	raw := fixture(t, "latin.diff")
	if _, err := run(t, []string{"git", "diff"}, strings.Repeat(raw, 80)); err != format.ErrTierInapplicable {
		t.Fatalf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestDiffBinaryFilesLineSurvives(t *testing.T) {
	t.Setenv(difffloor.EnvVar, "0")
	raw := fixture(t, "bin.diff")
	out, err := run(t, []string{"git", "diff"}, raw)
	if err == nil && !strings.Contains(string(out.Body), "Binary files a/b.bin and b/b.bin differ") {
		t.Fatalf("binary marker lost: %q", out.Body)
	}
}
