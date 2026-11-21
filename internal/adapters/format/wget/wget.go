// Package wget formats non-interactive `wget` runs. wget logs to stderr and,
// when that is not a terminal, prints one dot-progress line per 50K received —
// a 26 MB download is ~550 lines that say nothing the final "saved" line does
// not. Each run of progress lines collapses to its last line (which carries
// the 100% mark and the average speed) plus a counted marker. Every other line
// (resolve/connect/HTTP status, redirects, "saved [n/n]", errors) is kept
// verbatim, so the saved path, status and failures survive untouched.
package wget

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// progressRE matches a dot-style progress row: an offset ("0K", "26850K",
// "1G") followed by dot/space clusters and, on all but the final partial row
// of a block, a percentage and rate.
var progressRE = regexp.MustCompile(`^\s*\d+[KMGT]\s+[.,\s]+(\s+\d+%.*)?$`)

// minRun is the shortest run of progress rows worth collapsing.
const minRun = 3

// Formatter renders wget output.
type Formatter struct{}

// New constructs a wget Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every wget invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "wget"} }

// Aggressive collapses dot-progress runs.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed behaves like Aggressive: the progress grammar is fixed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// toStdout reports whether the downloaded body itself goes to stdout
// (`-O -`), in which case stdout is payload and must not be touched.
func toStdout(argv []string) bool {
	for i, a := range argv {
		switch {
		case a == "-O" && i+1 < len(argv) && argv[i+1] == "-":
			return true
		case a == "-O-", a == "--output-document=-":
			return true
		}
	}
	return false
}

func render(in format.Input) (format.Rendered, error) {
	if toStdout(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	lines := append(splitLines(rawOut), splitLines(rawErr)...)
	var out []string
	collapsed := false
	for i := 0; i < len(lines); {
		if !progressRE.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && progressRE.MatchString(lines[j]) {
			j++
		}
		if n := j - i; n >= minRun {
			out = append(out, fmt.Sprintf("…+%d progress lines", n-1), lines[j-1])
			collapsed = true
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	if !collapsed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
