// Package cc formats clang, gcc and their cc/c++ aliases. A diagnostic keeps
// its "path:line:col: severity: message" line and loses the source excerpt
// and caret lines under it, which restate code the agent can read from the
// file. The dropped lines are counted in a trailing marker.
package cc

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders one compiler driver's output.
type Formatter struct{ command string }

// All returns one formatter per supported driver name; the registry holds one
// entry per program.
func All() []format.Formatter {
	names := []string{"clang", "clang++", "gcc", "g++", "cc", "c++"}
	out := make([]format.Formatter, len(names))
	for i, n := range names {
		out[i] = &Formatter{command: n}
	}
	return out
}

// Descriptor claims every invocation of the driver.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

// Aggressive drops excerpts, folds repeated diagnostics and adds a warning
// flag histogram.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops excerpts only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !textOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := ReadAll(in.Stdout), ReadAll(in.Stderr)
	lines := append(SplitLines(rawOut), SplitLines(rawErr)...)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, st := Filter(lines, aggressive)
	if st.Dropped == 0 && st.Deduped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, Footer(st, aggressive)...)
	body := strings.Join(kept, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

// textOutput reports whether the compiler's streams are its human diagnostics.
// Preprocessor/assembly to stdout, dependency output, version/help text and
// verbose search-path dumps are the user's requested payload, not diagnostics.
func textOutput(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch a {
		case "-E", "-M", "-MM", "-dM", "-v", "--version", "-version", "-###", "--help", "-help",
			"-dumpmachine", "-dumpversion", "-ast-dump", "-emit-llvm":
			return false
		case "-o":
			if i+1 < len(argv) && argv[i+1] == "-" {
				return false
			}
		}
		if strings.HasPrefix(a, "-print-") || strings.HasPrefix(a, "-fdiagnostics-format") ||
			strings.HasPrefix(a, "-Xclang") {
			return false
		}
	}
	return true
}

// ReadAll drains a possibly-nil reader.
func ReadAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

// SplitLines splits raw into lines without their terminators.
func SplitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
