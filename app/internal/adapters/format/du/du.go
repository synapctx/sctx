// Package du implements a format.Formatter for `du`. Recursive `du -h` /
// `du -ah` output can run hundreds of lines; the aggressive tier sorts by
// size descending (largest paths first, since that's what an agent chasing
// disk usage wants) and caps to the biggest rows.
package du

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `du` output.
type Formatter struct{}

// New constructs a du Formatter.
func New() format.Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "du"}
}

const (
	maxRows          = 40
	trivialLineCount = 3
)

// sizeRe matches a du size token: a number optionally followed by a unit
// suffix (K, M, G, T, P, optionally with a trailing "B" or "iB", or no
// suffix at all for raw 1K-block counts).
var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([A-Za-z]*)$`)

// entry is one parsed du data row.
type entry struct {
	sizeStr string
	path    string
	bytes   float64
}

// parseSize converts a du size token into a comparable byte count. A bare
// number (no unit suffix) is du's default: 1K-block counts.
func parseSize(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	num, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	unit := m[2]
	if unit == "" {
		return num * 1024, true // raw 1K-block count
	}
	u := strings.ToUpper(unit)
	u = strings.TrimSuffix(u, "IB")
	u = strings.TrimSuffix(u, "B")
	var mult float64
	switch u {
	case "":
		mult = 1 // bytes
	case "K":
		mult = 1024
	case "M":
		mult = 1024 * 1024
	case "G":
		mult = 1024 * 1024 * 1024
	case "T":
		mult = 1024 * 1024 * 1024 * 1024
	case "P":
		mult = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0, false
	}
	return num * mult, true
}

// parseLine splits a du output line into its size token and path. The path
// keeps everything after the first run of whitespace, including embedded
// spaces.
func parseLine(line string) (sizeStr, path string, ok bool) {
	trimmed := strings.TrimRight(line, "\r")
	idx := strings.IndexAny(trimmed, " \t")
	if idx == -1 {
		return "", "", false
	}
	sizeStr = trimmed[:idx]
	path = strings.TrimLeft(trimmed[idx:], " \t")
	if path == "" {
		return "", "", false
	}
	if _, ok := parseSize(sizeStr); !ok {
		return "", "", false
	}
	return sizeStr, path, true
}

// Aggressive parses du's SIZE<ws>PATH lines, sorts by size descending, and
// caps to the largest rows. Lines that don't parse (e.g. permission-error
// diagnostics some du builds emit on stdout) are preserved verbatim. A
// trailing GNU `du -c` grand-total line ("...\ttotal") is kept at the end,
// outside the cap.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("du: reading stdout: %w", err)
	}
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var entries []entry
	var keep []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sizeStr, path, ok := parseLine(line)
		if !ok {
			keep = append(keep, line)
			continue
		}
		b, _ := parseSize(sizeStr)
		entries = append(entries, entry{sizeStr: sizeStr, path: path, bytes: b})
	}

	if len(entries) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(entries)+len(keep) <= trivialLineCount {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var totalLine string
	if last := entries[len(entries)-1]; strings.EqualFold(strings.TrimSpace(last.path), "total") {
		totalLine = last.sizeStr + "\t" + last.path
		entries = entries[:len(entries)-1]
	}

	total := len(entries)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].bytes > entries[j].bytes })

	var extra int
	if len(entries) > maxRows {
		extra = total - maxRows
		entries = entries[:maxRows]
	}

	var b strings.Builder
	for _, l := range keep {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, e := range entries {
		b.WriteString(e.sizeStr)
		b.WriteByte('\t')
		b.WriteString(e.path)
		b.WriteByte('\n')
	}
	if extra > 0 {
		fmt.Fprintf(&b, "…+%d more paths\n", extra)
	}
	if totalLine != "" {
		b.WriteString(totalLine)
		b.WriteByte('\n')
	}

	return format.Rendered{
		Body: []byte(strings.TrimRight(b.String(), "\n")),
		Note: fmt.Sprintf("%d paths", total),
	}, nil
}

// Relaxed drops blank lines and exact-duplicate lines. du has no real noise
// to strip, so if nothing was removed this tier can't beat verbatim.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("du: reading stdout: %w", err)
	}
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	seen := make(map[string]bool, len(lines))
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) == 0 || len(out) == len(lines) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(strings.Join(out, "\n"))}, nil
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
