// Package gcloud formats `gcloud` output that is not a machine format.
//
//   - List/describe TABLES (gcloud pads every column to its widest cell) are
//     re-emitted tab-separated, cut at the header's column offsets so an empty
//     cell stays an empty cell. The cut is verified on every row; a table that
//     does not align declines.
//   - API errors print the same message three times (the ERROR line, the bare
//     message, a link). A line that is an exact substring of the ERROR line is
//     replaced by one counted marker.
//   - "Maybe you meant:" suggestion lists keep their first entries.
//   - `--help` pages lose their ANSI bold/underline.
//
// --format=json/yaml/value|csv|text output declines (JSON reaches the lossless
// fallback). Stdout and stderr are both consumed, and stderr is folded.
package gcloud

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders gcloud output.
type Formatter struct{}

// New returns the gcloud formatter.
func New() format.Formatter { return &Formatter{} }

// Descriptor claims every gcloud invocation.
func (*Formatter) Descriptor() format.Match { return format.Match{Command: "gcloud"} }

const (
	suggestKeep = 8
	minDupLen   = 20
)

var (
	ansiRE   = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	headerRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(  +[A-Z][A-Z0-9_]*)*\s*$`)
)

// Aggressive applies every transformation.
func (*Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed applies only the lossless ones (tables, ANSI).
func (*Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

// machineFormat reports an explicit --format that is not the default table.
func machineFormat(argv []string) bool {
	for i, a := range argv[1:] {
		v := ""
		switch {
		case strings.HasPrefix(a, "--format="):
			v = strings.TrimPrefix(a, "--format=")
		case a == "--format" && i+2 < len(argv):
			v = argv[i+2]
		default:
			continue
		}
		if !strings.HasPrefix(v, "table") {
			return true
		}
	}
	return false
}

func readAll(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	b, err := io.ReadAll(r)
	return string(b), err
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 || machineFormat(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gcloud: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gcloud: reading stderr: %w", err)
	}
	if stdout == "" && stderr == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	changed := false
	if stdout != "" {
		if s := stripANSI(stdout); s != stdout {
			stdout, changed = s, true
		}
		if t, ok := tabulate(stdout); ok {
			stdout, changed = t, true
		}
	}
	elided := false
	if stderr != "" {
		s := stripANSI(stderr)
		if aggressive {
			var e bool
			s, e = dedupeError(s)
			elided = e
		}
		if s != stderr {
			changed = true
		}
		stderr = s
	}
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	b.WriteString(stdout)
	if stdout != "" && !strings.HasSuffix(stdout, "\n") {
		b.WriteByte('\n')
	}
	fold := false
	if stderr != "" {
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
		fold = true
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: elided}, nil
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// tabulate re-emits an aligned gcloud table with tab separators. It declines
// (ok=false) unless the first line is an uppercase header and every row has a
// space (or the row's end) just before each column offset.
func tabulate(s string) (string, bool) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) < 2 || !headerRE.MatchString(lines[0]) {
		return "", false
	}
	hdr := strings.TrimRight(lines[0], " ")
	var offs []int
	for i := 0; i < len(hdr); i++ {
		if hdr[i] != ' ' && (i == 0 || (hdr[i-1] == ' ' && i >= 2 && hdr[i-2] == ' ')) {
			offs = append(offs, i)
		}
	}
	if len(offs) < 2 {
		return "", false
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimRight(l, " \r")
		if strings.Contains(l, "\t") {
			return "", false
		}
		cells := make([]string, len(offs))
		for c, o := range offs {
			if o >= len(l) {
				break
			}
			if o > 0 && l[o-1] != ' ' {
				return "", false
			}
			end := len(l)
			if c+1 < len(offs) {
				end = min(offs[c+1], len(l))
			}
			cells[c] = strings.TrimRight(l[o:end], " ")
		}
		out = append(out, strings.TrimRight(strings.Join(cells, "\t"), "\t"))
	}
	r := strings.Join(out, "\n") + "\n"
	if len(r) >= len(s) {
		return "", false
	}
	return r, true
}

// dedupeError removes stderr lines that are exact substrings of an earlier
// ERROR: line, and caps "Maybe you meant:" suggestion lists. Each removal site
// carries a counted marker.
func dedupeError(s string) (string, bool) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var errLines []string
	for _, l := range lines {
		if strings.HasPrefix(l, "ERROR: ") {
			errLines = append(errLines, l)
		}
	}
	if len(errLines) == 0 {
		return s, false
	}
	isDup := func(l string) bool {
		if len(l) < minDupLen || strings.HasPrefix(l, "ERROR: ") {
			return false
		}
		for _, e := range errLines {
			if strings.Contains(e, strings.TrimSpace(l)) {
				return true
			}
		}
		return false
	}
	var out []string
	dups, markAt := 0, -1
	elided := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l == "Maybe you meant:" {
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], "  gcloud ") {
				j++
			}
			n := j - i - 1
			out = append(out, l)
			if n > suggestKeep {
				out = append(out, lines[i+1:i+1+suggestKeep]...)
				out = append(out, fmt.Sprintf("  …+%d more suggestions", n-suggestKeep))
				elided = true
			} else {
				out = append(out, lines[i+1:j]...)
			}
			i = j - 1
			continue
		}
		if isDup(l) {
			if markAt < 0 {
				markAt = len(out)
				out = append(out, "")
			}
			dups++
			continue
		}
		out = append(out, l)
	}
	if dups > 0 {
		out[markAt] = fmt.Sprintf("…+%d lines (text repeated from the ERROR line)", dups)
		elided = true
	}
	return strings.Join(out, "\n") + "\n", elided
}
