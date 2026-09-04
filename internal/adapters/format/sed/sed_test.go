package sed

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func input(argv []string, stdout string, exit int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(stdout), ExitCode: exit}
}

// repeatFixture is `sed -n '1,7p' log.txt` output captured from the real
// darwin `sed` binary: a repeated line (4x) followed by three distinct
// lines, exercising read's relaxed dedupe-collapse path.
const repeatFixture = "INFO ready\nINFO ready\nINFO ready\nINFO ready\nline-2\nline-3\nline-4\n"

func TestRecognizedShapesDelegateToRead(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{"range address", []string{"sed", "-n", "1,7p", "log.txt"}},
		{"regex address", []string{"sed", "-n", "/needle/p", "log.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New()
			in := input(tt.argv, repeatFixture, 0)
			out, err := f.Relaxed(context.Background(), in)
			if err != nil {
				t.Fatalf("Relaxed() error = %v", err)
			}
			if len(out.Body) == 0 || len(out.Body) >= len(repeatFixture) {
				t.Fatalf("Relaxed() body not smaller: got %d bytes from %d raw", len(out.Body), len(repeatFixture))
			}
			if !strings.Contains(string(out.Body), "×4") {
				t.Fatalf("Relaxed() body missing the elision marker: %q", out.Body)
			}
		})
	}
}

func TestUnrecognizedShapesDecline(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{"substitution", []string{"sed", "s/a/b/", "file.txt"}},
		{"in-place edit", []string{"sed", "-i", "", "s/a/b/", "file.txt"}},
		{"no -n", []string{"sed", "1,7p", "file.txt"}},
		{"missing file", []string{"sed", "-n", "1,7p"}},
		{"multiple files", []string{"sed", "-n", "1,7p", "a.txt", "b.txt"}},
		{"file looks like a flag", []string{"sed", "-n", "1,7p", "-x"}},
		{"unrecognised expression", []string{"sed", "-n", "1,7d", "file.txt"}},
		{"unbounded pattern", []string{"sed", "-n", "1,$p", "file.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New()
			in := input(tt.argv, repeatFixture, 0)
			if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("Aggressive() error = %v, want ErrTierInapplicable", err)
			}
			if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("Relaxed() error = %v, want ErrTierInapplicable", err)
			}
		})
	}
}

// TestNonZeroExitDeclinesToVerbatim mirrors real sed: a missing file exits 1
// with a diagnostic on stderr, and read's own aggressive tier declines on
// any non-zero exit so that diagnostic passes through unmodified.
func TestNonZeroExitDeclinesToVerbatim(t *testing.T) {
	f := New()
	in := input([]string{"sed", "-n", "1,7p", "missing.txt"}, "", 1)
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("Aggressive() error = %v, want ErrTierInapplicable", err)
	}
}
