package mongosh

import (
	"context"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Relaxed strips the connection banner, then applies generic line-level
// filtering shared by every mongosh invocation: blank lines are dropped,
// leading indentation is collapsed, and consecutive duplicate lines are
// deduped to "line ×N". Everything else (including error text) is kept
// verbatim, never dropped. If banner-stripping and filtering together
// bought nothing (output is the same size or larger than the raw input),
// the tier is inapplicable and the chain degrades to verbatim.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("mongosh: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("mongosh: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	cleanedOut := stripBanner(stdout)
	cleanedErr := stripBanner(stderr)

	outLines := relaxedFilterLines(string(cleanedOut))
	errLines := relaxedFilterLines(string(cleanedErr))

	var b strings.Builder
	for _, l := range outLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	foldStderr := false
	if len(errLines) > 0 {
		for _, l := range errLines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		foldStderr = true
	} else if len(stderr) > 0 {
		// stderr was present but reduced to nothing but banner noise; it's
		// safe to fold away rather than re-emit it raw.
		foldStderr = true
	}

	body := strings.TrimRight(b.String(), "\n")
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	origSize := len(stdout) + len(stderr)
	if len(body)+1 >= origSize {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(body + "\n"), FoldStderr: foldStderr}, nil
}

// relaxedFilterLines drops blank lines, collapses each line's leading
// indentation, and collapses runs of consecutive identical lines into a
// single "line ×N" line.
func relaxedFilterLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")

	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, strings.TrimLeft(l, " \t"))
	}

	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		l := lines[i]
		j := i + 1
		for j < len(lines) && lines[j] == l {
			j++
		}
		n := j - i
		if n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", l, n))
		} else {
			out = append(out, l)
		}
		i = j
	}
	return out
}
