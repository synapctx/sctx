package run

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// readingFormatter reads stdout in its aggressive tier and then declines — the
// ordinary shape of a formatter, because you cannot tell whether output is yours
// without looking at it.
type readingFormatter struct {
	relaxedSaw  int
	stderrSaw   int
	relaxedBody string
}

func (f *readingFormatter) Descriptor() format.Match { return format.Match{Command: "probe"} }

func (f *readingFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	_, _ = io.ReadAll(in.Stdout)
	_, _ = io.ReadAll(in.Stderr)
	return format.Rendered{}, format.ErrTierInapplicable
}

func (f *readingFormatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	b, _ := io.ReadAll(in.Stdout)
	e, _ := io.ReadAll(in.Stderr)
	f.relaxedSaw, f.stderrSaw = len(b), len(e)
	if len(b) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(f.relaxedBody)}, nil
}

// EVERY TIER MUST GET ITS OWN READERS, and this is the test that says so.
//
// One Input was shared across the whole chain, so the first tier to READ stdout
// left the next one an empty stream. Any formatter that read before deciding —
// which is most of them — therefore had a DEAD relaxed tier, reported by the
// chain as the innocuous-sounding "no tier handles this invocation".
//
// It survived because unit tests build a fresh Input per tier: each tier passes
// in isolation and only the COMPOSITION fails, which is exactly why this test
// drives renderChain rather than a formatter directly. Measured cost before the
// fix: `make` 167 runs at 0% saved with 101 declining, `ssh` declining 171 of
// 176, and the generic fallback's line collapser unreachable in production while
// green in its own package.
func TestEveryTierGetsItsOwnReaders(t *testing.T) {
	raw := []byte("aaaa\nbbbb\ncccc\ndddd\n")
	stderr := []byte("warn\n")
	f := &readingFormatter{relaxedBody: "ok"}

	in := format.Input{Stdout: bytes.NewReader(raw), Stderr: bytes.NewReader(stderr)}
	got := renderChain(context.Background(), f, in, raw, stderr, "", nil)

	if f.relaxedSaw != len(raw) {
		t.Errorf("relaxed tier saw %d of %d stdout bytes; the aggressive tier consumed the reader",
			f.relaxedSaw, len(raw))
	}
	if f.stderrSaw != len(stderr) {
		t.Errorf("relaxed tier saw %d of %d stderr bytes", f.stderrSaw, len(stderr))
	}
	if got.Tier != format.TierRelaxed {
		t.Errorf("tier = %s, want relaxed: a working relaxed tier was skipped", got.Tier)
	}
}
