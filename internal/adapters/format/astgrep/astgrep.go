// Package astgrep formats `ast-grep scan` (and its `sg` alias) rule
// diagnostics: each finding keeps "file:line:col: severity[rule]: message" and
// its "= note" line, and loses the box-drawing source excerpt underneath
// (counted in a trailing marker). `ast-grep run` already prints compact
// "file:line:text" lines and is left alone.
package astgrep

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

var (
	headRE = regexp.MustCompile(`^(error|warning|info|hint)\[([^\]]+)\]: (.*)$`)
	locRE  = regexp.MustCompile(`^\s+[╭┌]─ (\S+:\d+:\d+)$`)
	noteRE = regexp.MustCompile(`^\s+= (.*)$`)
)

// Formatter renders one ast-grep program name.
type Formatter struct{ command string }

// All returns the ast-grep and sg formatters.
func All() []format.Formatter {
	return []format.Formatter{&Formatter{command: "ast-grep"}, &Formatter{command: "sg"}}
}

// Descriptor claims `scan` only.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: f.command, Subcommands: []string{"scan"}}
}

// Aggressive renders one line per finding.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed behaves like Aggressive: the block grammar is fixed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// userChoseOutput reports flags that select machine output or an explicit
// report style; the user's choice stands.
func userChoseOutput(argv []string) bool {
	for _, a := range argv[1:] {
		before, _, _ := strings.Cut(a, "=")
		switch before {
		case "--json", "--inspect", "--help", "-h", "--report-style", "--format":
			return true
		}
	}
	return false
}

func render(in format.Input) (format.Rendered, error) {
	if userChoseOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	lines := append(splitLines(rawOut), splitLines(rawErr)...)
	var out []string
	dropped, findings := 0, 0
	for i := 0; i < len(lines); i++ {
		h := headRE.FindStringSubmatch(lines[i])
		if h == nil || i+1 >= len(lines) {
			out = append(out, lines[i])
			continue
		}
		loc := locRE.FindStringSubmatch(lines[i+1])
		if loc == nil {
			out = append(out, lines[i])
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s[%s]: %s", loc[1], h[1], h[2], h[3]))
		findings++
		j := i + 2
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != ""; j++ {
			if n := noteRE.FindStringSubmatch(lines[j]); n != nil {
				out = append(out, " = "+n[1])
				continue
			}
			dropped++
		}
		i = j // the blank separator line
	}
	if findings == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out = append(out, fmt.Sprintf("…+%d source/caret lines", dropped))
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
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
