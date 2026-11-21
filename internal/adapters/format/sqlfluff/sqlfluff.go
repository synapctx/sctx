// Package sqlfluff implements a format.Formatter for sqlfluff's human lint and
// fix reports. Each violation is printed over two or three padded lines
// ("L:   1 | P:  10 | LT01 | message" plus "| continuation" lines carrying the
// rule alias); the rendering keeps one line per violation with its location,
// rule code and the full message.
package sqlfluff

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders sqlfluff lint and fix output.
type Formatter struct{}

// New constructs the sqlfluff Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every sqlfluff invocation; subcommand dispatch is
// internal because only lint/fix/format share this report shape.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "sqlfluff"} }

// Aggressive renders the per-file violation lists.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed shares the aggressive rendering; the report is line oriented and
// fully understood.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

var (
	fileRe  = regexp.MustCompile(`^== \[(.+)\] (\w+)$`)
	violRe  = regexp.MustCompile(`^L:\s*(\d+) \| P:\s*(\d+) \|\s+(\S+) \| (.*)$`)
	contRe  = regexp.MustCompile(`^\s+\| (.*)$`)
	aliasRe = regexp.MustCompile(`\s*\[[a-z_]+(?:\.[a-z_]+)+\]$`)
)

func supported(argv []string) bool {
	sub := ""
	for _, a := range argv[1:] {
		if !strings.HasPrefix(a, "-") {
			sub = a
			break
		}
	}
	if sub != "lint" && sub != "fix" && sub != "format" {
		return false
	}
	for _, a := range argv[1:] {
		switch {
		case a == "--format" || strings.HasPrefix(a, "--format="),
			a == "--write-output" || strings.HasPrefix(a, "--write-output="),
			a == "--annotation-level" || a == "--bench" || a == "--help":
			return false
		case a == "-f" && sub == "lint":
			return false
		}
	}
	return true
}

type violation struct {
	line, col, code, msg string
}

type fileBlock struct {
	path, status string
	viols        []*violation
}

func render(in format.Input) (format.Rendered, error) {
	if !supported(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var sout, serr []byte
	if in.Stdout != nil {
		sout, _ = io.ReadAll(in.Stdout)
	}
	if in.Stderr != nil {
		serr, _ = io.ReadAll(in.Stderr)
	}
	text := strings.TrimRight(string(sout), "\r\n")
	if text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var blocks []*fileBlock
	var cur *fileBlock
	var lastViol *violation
	var other []string
	finished := false
	for _, l := range strings.Split(text, "\n") {
		switch {
		case fileRe.MatchString(l):
			m := fileRe.FindStringSubmatch(l)
			cur = &fileBlock{path: m[1], status: m[2]}
			blocks = append(blocks, cur)
			lastViol = nil
		case violRe.MatchString(l) && cur != nil:
			m := violRe.FindStringSubmatch(l)
			lastViol = &violation{line: m[1], col: m[2], code: m[3], msg: m[4]}
			cur.viols = append(cur.viols, lastViol)
		case contRe.MatchString(l) && lastViol != nil:
			lastViol.msg += " " + contRe.FindStringSubmatch(l)[1]
		case strings.HasPrefix(l, "All Finished"):
			finished = true
		case strings.TrimSpace(l) == "":
		default:
			other = append(other, l)
			lastViol = nil
		}
	}
	if len(blocks) == 0 && !finished {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	total := 0
	for _, fb := range blocks {
		if len(fb.viols) == 0 {
			fmt.Fprintf(&b, "%s %s\n", fb.path, fb.status)
			continue
		}
		fmt.Fprintf(&b, "%s %s (%d)\n", fb.path, fb.status, len(fb.viols))
		for _, v := range fb.viols {
			total++
			fmt.Fprintf(&b, "  L%s:%s %s %s\n", v.line, v.col, v.code, aliasRe.ReplaceAllString(v.msg, ""))
		}
	}
	for _, l := range other {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(blocks) == 0 || total == 0 && len(other) == 0 {
		b.WriteString("All Finished!")
	}
	_ = serr // sqlfluff's report is on stdout; stderr stays on the real stream
	return format.Rendered{Body: []byte(strings.TrimRight(b.String(), "\n")), FoldStderr: false}, nil
}
