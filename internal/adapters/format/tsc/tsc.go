// Package tsc implements a format.Formatter for the TypeScript compiler. It
// parses both the classic `file(l,c): error TSxxxx: msg` form and the pretty
// `file:l:c - error TSxxxx: msg` form, groups diagnostics per file, drops the
// pretty form's code-frame snippets (counted) and adds a per-code histogram.
package tsc

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

const (
	// manyFiles is the file count above which per-file output is capped.
	manyFiles = 20
	// perFileCap is the diagnostics kept per file once manyFiles is exceeded.
	perFileCap = 10
)

var (
	classicRE = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): error (TS\d+): (.*)$`)
	prettyRE  = regexp.MustCompile(`^(.+?):(\d+):(\d+) - error (TS\d+): (.*)$`)
	globalRE  = regexp.MustCompile(`^error (TS\d+): (.*)$`)
	foundRE   = regexp.MustCompile(`^Found (\d+) errors?( in .*)?\.?$`)
	tableRE   = regexp.MustCompile(`^\s*(Errors\s+Files|\d+\s+\S.*)$`)
)

// Formatter renders tsc output.
type Formatter struct{}

// New constructs a tsc Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every tsc invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "tsc"} }

// Aggressive parses diagnostics and renders the grouped listing. Output
// recognition decides applicability, never the exit code.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return Render(in)
}

// Relaxed collapses provably redundant runs only.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr := jsutil.Streams(in)
	outLines, c1 := collapse.Runs(collapse.SplitLines(stdout))
	errLines, c2 := collapse.Runs(collapse.SplitLines(stderr))
	if !c1 && !c2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append(append([]string(nil), outLines...), errLines...)
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0}, nil
}

type diag struct {
	line, col, code, msg string
}

type parsed struct {
	files      []string
	lastFile   string
	byFile     map[string][]diag
	total      int
	frameLines int
	other      []string
	reported   int // "Found N errors", -1 when absent
}

// Render is the aggressive tier over a captured invocation. It is exported so
// the npx/npm formatters can delegate to it when they recognise tsc output.
func Render(in format.Input) (format.Rendered, error) {
	stdout, stderr := jsutil.Streams(in)
	p := parsed{byFile: map[string][]diag{}, reported: -1}
	p.parse(jsutil.Lines(stdout))
	p.parse(jsutil.Lines(stderr))
	if p.total == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if p.reported >= 0 && p.reported != p.total {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return p.render(len(stderr) > 0), nil
}

func (p *parsed) add(file string, d diag) {
	if _, ok := p.byFile[file]; !ok {
		p.files = append(p.files, file)
	}
	p.byFile[file] = append(p.byFile[file], d)
	p.lastFile = file
	p.total++
}

func (p *parsed) parse(lines []string) {
	inFrame := false // inside a pretty code frame, after the diagnostic header
	inDiag := false  // the previous kept line belongs to a diagnostic
	for _, l := range lines {
		if m := classicRE.FindStringSubmatch(l); m != nil {
			p.add(m[1], diag{m[2], m[3], m[4], m[5]})
			inFrame, inDiag = false, true
			continue
		}
		if m := prettyRE.FindStringSubmatch(l); m != nil {
			p.add(m[1], diag{m[2], m[3], m[4], m[5]})
			inFrame, inDiag = false, true
			continue
		}
		if m := globalRE.FindStringSubmatch(l); m != nil {
			p.add("", diag{"", "", m[1], m[2]})
			inFrame, inDiag = false, true
			continue
		}
		if m := foundRE.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			p.reported = n
			inFrame, inDiag = false, false
			continue
		}
		if strings.TrimSpace(l) == "" {
			if inDiag {
				inFrame = true
			}
			continue
		}
		if inFrame {
			if c := l[0]; c == ' ' || c == '\t' || (c >= '0' && c <= '9') {
				// Code snippet, underline or related-information frame.
				p.frameLines++
				continue
			}
			inFrame = false
		}
		if inDiag && (l[0] == ' ' || l[0] == '\t') {
			// Message continuation: attach to the last diagnostic.
			p.appendContinuation(strings.TrimSpace(l))
			continue
		}
		inDiag = false
		if tableRE.MatchString(l) && p.reported >= 0 {
			continue // the per-file table after "Found N errors"
		}
		p.other = append(p.other, l)
	}
}

// appendContinuation attaches an indented message line (tsc nests the
// elaboration of a long diagnostic) to the diagnostic added last.
func (p *parsed) appendContinuation(s string) {
	ds := p.byFile[p.lastFile]
	ds[len(ds)-1].msg += "\n" + s
}

func (p *parsed) render(foldStderr bool) format.Rendered {
	capped := len(p.files) > manyFiles
	codes := map[string]int{}
	var b strings.Builder
	elided := false
	for _, file := range p.files {
		ds := p.byFile[file]
		name := file
		if name == "" {
			name = "(global)"
		}
		fmt.Fprintf(&b, "%s — %d %s\n", name, len(ds), jsutil.Plural(len(ds), "error", "errors"))
		for i, d := range ds {
			codes[d.code]++
			if capped && i >= perFileCap {
				continue
			}
			writeDiag(&b, d)
		}
		if capped && len(ds) > perFileCap {
			fmt.Fprintf(&b, "  …+%d more in file\n", len(ds)-perFileCap)
			elided = true
		}
	}
	fmt.Fprintf(&b, "%d %s in %d %s\n", p.total, jsutil.Plural(p.total, "error", "errors"),
		len(p.files), jsutil.Plural(len(p.files), "file", "files"))
	b.WriteString(jsutil.Histogram("codes", codes))
	if p.frameLines > 0 {
		fmt.Fprintf(&b, "\n…+%d code-frame lines", p.frameLines)
	}
	if len(p.other) > 0 {
		b.WriteString("\nother output:")
		for _, l := range p.other {
			b.WriteString("\n" + l)
		}
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: foldStderr, Elided: elided || p.frameLines > 0}
}

func writeDiag(b *strings.Builder, d diag) {
	head, rest, multi := strings.Cut(d.msg, "\n")
	if d.line == "" {
		fmt.Fprintf(b, "  %s: %s\n", d.code, head)
	} else {
		fmt.Fprintf(b, "  L%s:%s %s: %s\n", d.line, d.col, d.code, head)
	}
	if multi {
		for _, l := range strings.Split(rest, "\n") {
			fmt.Fprintf(b, "    %s\n", l)
		}
	}
}
