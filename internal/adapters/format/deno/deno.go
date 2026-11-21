// Package deno implements format.Formatter for `deno test`, `deno lint` and
// `deno check`. deno colours everything, prints a line per passing test,
// repeats every failure in a trailing FAILURES list, shows the assertion's own
// source line and caret, and walks library stack frames.
//
// The render keeps: the totals line, each failing test with its location,
// error, diff and first user stack frame, any output a test printed, and for
// lint/check every diagnostic with file:line:col, rule/code and message.
// Download progress, per-test ok lines, source/caret lines and library frames
// are dropped or counted behind explicit markers.
//
// Watch/server modes (`--watch`, `--hot`, `deno run`, `deno serve`, `deno task
// dev`) are never claimed here; `deno fmt` and the rest keep their own output.
package deno

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

// outputCap bounds the lines kept from one test's printed output.
const outputCap = 20

var (
	downloadRE  = regexp.MustCompile(`^(Download|Check|Compile|Initialize) \S+`)
	runningRE   = regexp.MustCompile(`^running \d+ tests? from \S+`)
	testLineRE  = regexp.MustCompile(`^(.+?) \.\.\. (ok|FAILED|ignored|cancelled)(?: \(.*\))?$`)
	testStartRE = regexp.MustCompile(`^(.+?) \.\.\.$`)
	outBeginRE  = regexp.MustCompile(`^-+ output -+$`)
	outEndRE    = regexp.MustCompile(`^-+ output end -+$`)
	errorsRE    = regexp.MustCompile(`^ERRORS$`)
	failuresRE  = regexp.MustCompile(`^FAILURES$`)
	failHeadRE  = regexp.MustCompile(`^(.+?) => (\S+?):(\d+):(\d+)$`)
	totalsRE    = regexp.MustCompile(`^(ok|FAILED) \| (.*?)(?: \(([^)]+)\))?$`)
	atRE        = regexp.MustCompile(`^\s+at (?:.*? \()?(\S+?):(\d+):(\d+)\)?$`)
	lintHeadRE  = regexp.MustCompile(`^error\[([^\]]+)\]: (.*)$`)
	lintLocRE   = regexp.MustCompile(`^\s+--> (\S+?):(\d+):(\d+)$`)
	lintHintRE  = regexp.MustCompile(`^\s+= hint: (.*)$`)
	lintSumRE   = regexp.MustCompile(`^(Found \d+ problems?|Checked \d+ files?)\b.*$`)
	tsHeadRE    = regexp.MustCompile(`^(TS\d+) \[ERROR\]: (.*)$`)
	tsFoundRE   = regexp.MustCompile(`^Found \d+ errors?\.$`)
)

// Formatter renders one deno subcommand's output.
type Formatter struct{ sub string }

// NewTest constructs the `deno test` formatter.
func NewTest() *Formatter { return &Formatter{sub: "test"} }

// NewLint constructs the `deno lint` formatter.
func NewLint() *Formatter { return &Formatter{sub: "lint"} }

// NewCheck constructs the `deno check` formatter.
func NewCheck() *Formatter { return &Formatter{sub: "check"} }

// All returns the test, lint and check formatters, one registry entry each.
func All() []format.Formatter { return []format.Formatter{NewTest(), NewLint(), NewCheck()} }

// Descriptor claims `deno <sub>`.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "deno", Subcommands: []string{f.sub}}
}

// Aggressive renders the compact view.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv {
		switch {
		case a == "--watch", strings.HasPrefix(a, "--watch="), a == "--hot", strings.HasPrefix(a, "--json"),
			strings.HasPrefix(a, "--reporter"), a == "--help", a == "-h", a == "--junit-path", a == "--compact":
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))
	var (
		body    string
		elided  bool
		decline bool
	)
	switch f.sub {
	case "test":
		body, elided, decline = renderTest(lines, in.ExitCode)
	case "lint":
		body, elided, decline = renderLint(lines, in.ExitCode)
	default:
		body, elided, decline = renderCheck(lines, in.ExitCode)
	}
	if decline {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(stderr) > 0, Elided: elided}, nil
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

// cleanPath drops the file:// scheme deno prints on stack frames.
func cleanPath(p string) string { return strings.TrimPrefix(p, "file://") }

type testFail struct {
	name, loc, file string
	msg             []string
	frame           string
	hidden          int
}

func renderTest(lines []string, exit int) (string, bool, bool) {
	var (
		totals    string
		fails     []*testFail
		cur       *testFail
		section   string // "", "errors", "failures"
		downloads int
		outBlocks []string
		inOut     bool
		outLines  []string
		outHidden int
		tail      []string
		sawTest   bool
		last      string // last test line's name, for attributing output
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if inOut {
			if outEndRE.MatchString(t) {
				inOut = false
				if len(outLines) > 0 {
					outBlocks = append(outBlocks, "output of "+last+":")
					for i, o := range outLines {
						if i >= outputCap {
							outHidden += len(outLines) - outputCap
							break
						}
						outBlocks = append(outBlocks, "  "+o)
					}
				}
				outLines = nil
				continue
			}
			outLines = append(outLines, l)
			continue
		}
		switch {
		case strings.HasPrefix(t, "Download "):
			downloads++
			continue
		case downloadRE.MatchString(t), runningRE.MatchString(t):
			continue
		case errorsRE.MatchString(t):
			section = "errors"
			continue
		case failuresRE.MatchString(t):
			section = "failures"
			cur = nil
			continue
		case outBeginRE.MatchString(t):
			inOut = true
			continue
		}
		if m := totalsRE.FindStringSubmatch(t); m != nil && strings.Contains(m[2], " passed") {
			totals = t
			section = ""
			cur = nil
			sawTest = true
			continue
		}
		if m := testLineRE.FindStringSubmatch(t); m != nil && section == "" {
			last = m[1]
			continue
		}
		if m := testStartRE.FindStringSubmatch(t); m != nil && section == "" {
			last = m[1]
			continue
		}
		if t == "error: Test failed" || strings.HasPrefix(t, "error: Test failed") {
			tail = append(tail, t)
			continue
		}
		switch section {
		case "errors":
			if m := failHeadRE.FindStringSubmatch(t); m != nil && !strings.HasPrefix(l, " ") {
				cur = &testFail{name: m[1], loc: m[2] + ":" + m[3] + ":" + m[4], file: m[2]}
				fails = append(fails, cur)
				continue
			}
			if cur == nil || t == "" {
				continue
			}
			if m := atRE.FindStringSubmatch(l); m != nil {
				if cur.frame == "" && jsutil.IsUserPath(m[1]) && !strings.HasPrefix(m[1], "http") {
					p := cleanPath(m[1])
					if base := strings.TrimPrefix(cur.file, "./"); base != "" && strings.HasSuffix(p, "/"+base) {
						p = cur.file
					}
					cur.frame = p + ":" + m[2] + ":" + m[3]
				} else {
					cur.hidden++
				}
				continue
			}
			if strings.HasPrefix(t, "throw new ") || t == "^" || strings.HasPrefix(t, "^") {
				cur.hidden++
				continue
			}
			cur.msg = append(cur.msg, strings.TrimRight(l, " "))
		case "failures":
			// Repeats the ERRORS headings; nothing new.
		default:
			if t != "" {
				tail = append(tail, t)
			}
		}
	}
	if !sawTest || totals == "" || inOut {
		return "", false, true
	}
	if exit != 0 && len(fails) == 0 {
		return "", false, true
	}
	var b strings.Builder
	b.WriteString("deno test: " + totals)
	hidden := 0
	for _, f := range fails {
		b.WriteString("\nFAIL " + f.name + " — " + f.loc)
		for _, m := range f.msg {
			n := 0
			for n < 4 && n < len(m) && m[n] == ' ' {
				n++
			}
			b.WriteString("\n  " + m[n:])
		}
		if f.frame != "" {
			b.WriteString("\n  at " + f.frame)
		}
		hidden += f.hidden
	}
	for _, o := range outBlocks {
		b.WriteString("\n" + o)
	}
	for _, o := range tail {
		b.WriteString("\n" + o)
	}
	hidden += outHidden
	if downloads > 0 {
		hidden += downloads
	}
	if hidden > 0 {
		b.WriteString("\n…+" + strconv.Itoa(hidden) + " frame/download/output lines")
	}
	return b.String(), hidden > 0, false
}

type diag struct {
	file, pos, code, msg, hint string
}

func commonDir(ds []diag) string {
	if len(ds) == 0 {
		return ""
	}
	dir := func(p string) string { return p[:strings.LastIndex(p, "/")+1] }
	pre := dir(ds[0].file)
	for _, d := range ds[1:] {
		for !strings.HasPrefix(d.file, pre) {
			pre = pre[:strings.LastIndex(strings.TrimSuffix(pre, "/"), "/")+1]
			if pre == "" {
				return ""
			}
		}
	}
	return pre
}

func renderDiags(kind string, ds []diag, summary []string, hidden int) string {
	pre := commonDir(ds)
	var b strings.Builder
	b.WriteString("deno " + kind + ": " + strconv.Itoa(len(ds)) + " " + jsutil.Plural(len(ds), "problem", "problems"))
	last := "\x00"
	for _, d := range ds {
		if d.file != last {
			b.WriteString("\n" + strings.TrimPrefix(d.file, pre))
			last = d.file
		}
		b.WriteString("\n  " + d.pos + " " + d.code)
		if d.msg != "" {
			b.WriteString(": " + d.msg)
		}
		if d.hint != "" {
			b.WriteString(" (hint: " + d.hint + ")")
		}
	}
	for _, s := range summary {
		b.WriteString("\n" + s)
	}
	if hidden > 0 {
		b.WriteString("\n…+" + strconv.Itoa(hidden) + " code-frame/docs lines")
	}
	return b.String()
}

func renderLint(lines []string, exit int) (string, bool, bool) {
	var (
		ds      []diag
		cur     *diag
		summary []string
		hidden  int
		other   []string
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if m := lintHeadRE.FindStringSubmatch(l); m != nil {
			ds = append(ds, diag{code: m[1], msg: m[2]})
			cur = &ds[len(ds)-1]
			continue
		}
		if m := lintLocRE.FindStringSubmatch(l); m != nil && cur != nil {
			cur.file, cur.pos = cleanPath(m[1]), m[2]+":"+m[3]
			continue
		}
		if m := lintHintRE.FindStringSubmatch(l); m != nil && cur != nil {
			cur.hint = m[1]
			continue
		}
		if lintSumRE.MatchString(t) {
			summary = append(summary, t)
			cur = nil
			continue
		}
		if t == "" {
			continue
		}
		if cur != nil {
			hidden++
			continue
		}
		if downloadRE.MatchString(t) {
			continue
		}
		other = append(other, t)
	}
	for _, d := range ds {
		if d.file == "" {
			// A diagnostic without a location line: not ours to summarise.
			return "", false, true
		}
	}
	if len(ds) == 0 || len(other) > 0 {
		return "", false, true
	}
	return renderDiags("lint", ds, summary, hidden), hidden > 0, false
}

func renderCheck(lines []string, exit int) (string, bool, bool) {
	var (
		ds      []diag
		cur     *diag
		summary []string
		hidden  int
		other   []string
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if m := tsHeadRE.FindStringSubmatch(t); m != nil {
			ds = append(ds, diag{code: m[1], msg: m[2]})
			cur = &ds[len(ds)-1]
			continue
		}
		if m := atRE.FindStringSubmatch(l); m != nil && cur != nil && cur.file == "" {
			cur.file, cur.pos = cleanPath(m[1]), m[2]+":"+m[3]
			continue
		}
		if tsFoundRE.MatchString(t) || strings.HasPrefix(t, "error: Type checking failed") {
			summary = append(summary, t)
			cur = nil
			continue
		}
		if t == "" {
			continue
		}
		if cur != nil {
			hidden++
			continue
		}
		if downloadRE.MatchString(t) {
			continue
		}
		other = append(other, t)
	}
	for _, d := range ds {
		if d.file == "" {
			return "", false, true
		}
	}
	if len(ds) == 0 || len(other) > 0 {
		return "", false, true
	}
	return renderDiags("check", ds, summary, hidden), hidden > 0, false
}
