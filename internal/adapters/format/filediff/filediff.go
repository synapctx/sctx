// Package filediff implements a format.Formatter for the `diff` CLI. It
// parses unified diff output (--- +++ @@ hunks), collapsing long runs of
// unchanged context lines while keeping every changed line and header.
package filediff

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `diff` output.
type Formatter struct{}

// New constructs a diff Formatter.
func New() *Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "diff"}
}

// contextRunThreshold is the minimum run length of unchanged context lines
// that gets collapsed (kept first+last, elided between).
const contextRunThreshold = 3

// Aggressive parses unified diff output, keeping file/hunk headers and every
// +/- changed line, and collapsing runs of 3+ unchanged context lines to
// first+last plus an explicit "…+N unchanged" marker. Non-unified (ed-style)
// output is already minimal and not applicable here. diff exits 1 when files
// differ — that's the normal case, not an error; only exit > 1 (a real diff
// failure) degrades.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode > 1 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("filediff: reading stdout: %w", err)
	}
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if !looksUnified(lines) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	kept, hunks, adds, dels := renderUnified(lines)
	if len(kept) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	summary := fmt.Sprintf("%d hunks, +%d -%d", hunks, adds, dels)
	body := strings.Join(append([]string{summary}, kept...), "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(body), Note: summary}, nil
}

// looksUnified reports whether lines contain at least one unified diff hunk
// header ("@@ ... @@").
func looksUnified(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, "@@") {
			return true
		}
	}
	return false
}

// renderUnified walks unified diff lines, keeping headers and changed lines
// verbatim and collapsing runs of 3+ context lines.
func renderUnified(lines []string) (kept []string, hunks, adds, dels int) {
	var contextRun []string
	flush := func() {
		if len(contextRun) == 0 {
			return
		}
		if len(contextRun) >= contextRunThreshold {
			kept = append(kept, contextRun[0])
			kept = append(kept, fmt.Sprintf("…+%d unchanged", len(contextRun)-2))
			kept = append(kept, contextRun[len(contextRun)-1])
		} else {
			kept = append(kept, contextRun...)
		}
		contextRun = nil
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			flush()
			kept = append(kept, line)
		case strings.HasPrefix(line, "@@"):
			flush()
			hunks++
			kept = append(kept, line)
		case strings.HasPrefix(line, "+"):
			flush()
			adds++
			kept = append(kept, line)
		case strings.HasPrefix(line, "-"):
			flush()
			dels++
			kept = append(kept, line)
		case strings.HasPrefix(line, " "):
			contextRun = append(contextRun, line)
		default:
			// "\ No newline at end of file", "diff --git", "Index:", etc.
			flush()
			kept = append(kept, line)
		}
	}
	flush()
	return kept, hunks, adds, dels
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// splitLines splits raw bytes into lines without trailing newlines.
func splitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
