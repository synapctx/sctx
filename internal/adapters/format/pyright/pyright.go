// Package pyright implements format.Formatters for the Python type checkers
// pyright, basedpyright and ty. pyright and basedpyright share one text
// report (a path header, indented "path:line:col - severity: message" lines,
// a count summary); ty prints rustc-style blocks with source snippets.
// Both are re-rendered per file with the path stated once, every diagnostic
// and its detail lines kept, and snippets reduced to their label text.
package pyright

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type kind int

const (
	kindPyright kind = iota
	kindTy
)

// Formatter renders one of pyright, basedpyright or ty.
type Formatter struct {
	command string
	kind    kind
}

// New constructs the pyright formatter.
func New() *Formatter { return &Formatter{command: "pyright", kind: kindPyright} }

// NewBased constructs the basedpyright formatter (pyright's output shape).
func NewBased() *Formatter { return &Formatter{command: "basedpyright", kind: kindPyright} }

// NewTy constructs the ty formatter.
func NewTy() *Formatter { return &Formatter{command: "ty", kind: kindTy} }

// All returns every formatter this package provides, for registration.
func All() []*Formatter { return []*Formatter{New(), NewBased(), NewTy()} }

// Descriptor claims every invocation of the formatter's program; unsupported
// subcommands and machine-output flags decline inside the tiers.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

// Aggressive renders the diagnostics grouped per file with long detail
// chains capped behind an explicit marker.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, maxDetailLines)
}

// Relaxed is the same rendering with no detail cap: lossless apart from the
// repeated path prefix and source snippets.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, 0)
}

func (f *Formatter) render(in format.Input, detailCap int) (format.Rendered, error) {
	if !applicable(f.kind, in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out := readAll(in.Stdout)
	errOut := readAll(in.Stderr)
	switch f.kind {
	case kindTy:
		// ty writes its diagnostics to stdout; progress/warnings go to stderr
		// and stay on the real stderr stream.
		return renderTy(out, detailCap)
	default:
		return renderPyright(out, errOut, detailCap)
	}
}

// applicable reports whether argv asks for the human text report.
func applicable(k kind, argv []string) bool {
	for i, a := range argv {
		if i == 0 {
			continue
		}
		switch a {
		case "--outputjson", "--verifytypes", "--version", "--help", "-h", "--watch", "-w",
			"--createstub", "--stats", "--dependencies", "server", "version", "--output-format=json",
			"--output-format=gitlab", "--output-format=github", "--output-format=concise":
			return false
		}
		if strings.HasPrefix(a, "--output-format") {
			return false
		}
	}
	if k == kindTy {
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			return a == "check"
		}
	}
	return true
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\r\n")
	if s == "" {
		return nil
	}
	ls := strings.Split(s, "\n")
	for i := range ls {
		ls[i] = strings.TrimSuffix(ls[i], "\r")
	}
	return ls
}

// getwd is replaceable in tests.
var getwd = os.Getwd

// relPath makes an absolute path relative to the working directory so it
// matches what the user types; anything outside the cwd is left as printed.
func relPath(p string) string {
	wd, err := getwd()
	if err != nil || wd == "" || !strings.HasPrefix(p, "/") {
		return p
	}
	if rest, ok := strings.CutPrefix(p, strings.TrimRight(wd, "/")+"/"); ok {
		return rest
	}
	return p
}
