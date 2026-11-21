// Package svn formats the per-file progress lines of Subversion's bulk verbs
// (checkout, update, switch, add, commit, import, export, revert). Each file
// line ("A    dir/file", "Adding   dir/file", "U    dir/file") says only that
// the file was touched, so the lines collapse into one counted line per
// (action, directory); lines that signal a problem (conflicts, tree conflicts,
// skips, errors) and every summary line ("Checked out revision N.",
// "Committed revision N.") stay verbatim and in place. `status`, `diff`,
// `log` and `info` are not progress output and are left alone.
package svn

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// minEntries is the smallest number of file lines worth grouping.
const minEntries = 20

var (
	// update/checkout/add columns: "A    path", " U   path", "UU   path"
	columnRE = regexp.MustCompile(`^([ADUGEMR ][ADUGEMR ]?[ BCU]?[ C]?)\s+(\S.*)$`)
	// commit verbs: "Adding         path"
	commitRE = regexp.MustCompile(`^(Adding|Sending|Deleting|Replacing)(?: +\(bin\))?\s+(\S.*)$`)
)

// bulkVerbs are the subcommands whose output is per-file progress.
var bulkVerbs = map[string]bool{
	"checkout": true, "co": true, "update": true, "up": true, "switch": true, "sw": true,
	"add": true, "commit": true, "ci": true, "import": true, "export": true, "revert": true,
}

// Formatter renders svn output.
type Formatter struct{}

// New constructs an svn Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every svn invocation; dispatch is internal.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "svn"} }

// Aggressive groups per-file progress lines.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed behaves like Aggressive: the line grammar is fixed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

func subcommand(argv []string) string {
	for _, a := range argv[min(1, len(argv)):] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

type group struct {
	action, dir string
	n           int
}

// fileLine returns the action label and path of a plain per-file progress
// line. Anything that could be a problem (a C in any column, E, or a
// non-trivial status) is not plain and stays verbatim.
func fileLine(l string) (action, p string, ok bool) {
	if m := commitRE.FindStringSubmatch(l); m != nil {
		return m[1], m[2], true
	}
	if m := columnRE.FindStringSubmatch(l); m != nil {
		cols := strings.TrimRight(m[1], " ")
		if strings.ContainsAny(cols, "CE") || cols == "" {
			return "", "", false
		}
		return cols, m[2], true
	}
	return "", "", false
}

func render(in format.Input) (format.Rendered, error) {
	if !bulkVerbs[subcommand(in.Argv)] {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	lines := append(splitLines(rawOut), splitLines(rawErr)...)

	var out []string
	var order []*group
	byKey := map[string]*group{}
	slot, files := -1, 0
	for _, l := range lines {
		action, p, ok := fileLine(l)
		if !ok {
			out = append(out, l)
			continue
		}
		files++
		if slot < 0 {
			slot = len(out)
			out = append(out, "")
		}
		dir := path.Dir(strings.TrimSuffix(p, "/"))
		key := action + "\x00" + dir
		g := byKey[key]
		if g == nil {
			g = &group{action: action, dir: dir}
			byKey[key] = g
			order = append(order, g)
		}
		g.n++
	}
	if files < minEntries {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	block := make([]string, 0, len(order))
	for _, g := range order {
		block = append(block, fmt.Sprintf("%s %s/ ×%d", g.action, g.dir, g.n))
	}
	out[slot] = strings.Join(block, "\n")
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
