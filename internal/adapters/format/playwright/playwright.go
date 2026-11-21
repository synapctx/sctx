// Package playwright implements format.Formatter for `playwright test`'s
// terminal reporters (list, line, dot). A passing run collapses to its summary;
// a failing run keeps, per failing test, the error message, locator/expected/
// received lines, the failing source position, and the paths of the attached
// screenshot, trace and error-context files. Code frames, per-test progress
// lines and the `show-trace` usage boilerplate are dropped behind explicit
// markers, and a test that failed identically on every retry is shown once
// with its attempt count.
//
// Other reporters (--reporter=html|json|junit|github|blob) are the user's own
// choice and decline.
package playwright

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

// callLogCap bounds one failure's "Call log:" lines.
const callLogCap = 8

var (
	failHeadRE  = regexp.MustCompile(`^  (\d+)\) (.+?) ─+$`)
	retryRE     = regexp.MustCompile(`^\s+Retry #(\d+) ─+$`)
	statRE      = regexp.MustCompile(`^  (\d+) (failed|flaky|skipped|did not run|interrupted|passed)(?: \((.+)\))?$`)
	codeLineRE  = regexp.MustCompile(`^\s*>?\s*\d+ \|`)
	caretRE     = regexp.MustCompile(`^\s+\|\s*\^`)
	attachRE    = regexp.MustCompile(`^\s+attachment #\d+: (.+?) \(([^)]*)\) ─+$`)
	ruleRE      = regexp.MustCompile(`^\s+─{10,}$`)
	atRE        = regexp.MustCompile(`^\s+at (\S+?):(\d+):(\d+)$`)
	ctxRE       = regexp.MustCompile(`^\s+Error Context: (\S+)$`)
	pathLineRE  = regexp.MustCompile(`^\s+(test-results/\S+|\S+/test-results/\S+)$`)
	showTraceRE = regexp.MustCompile(`^\s+(Usage:|npx playwright show-trace\b.*)$`)
	runningRE   = regexp.MustCompile(`^Running \d+ tests? using \d+ workers?`)
	progressRE  = regexp.MustCompile(`^\s*(?:[✓✘x\-·F×°]\s+\d+ |\[\d+/\d+\] |[·F×°T*]+$)`)
)

// Formatter renders `playwright test` output.
type Formatter struct{}

// New constructs the playwright formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims `playwright test`; install/codegen/show-report are not
// test output.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "playwright", Subcommands: []string{"test"}}
}

// Aggressive renders the summary-and-failures view.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return Render(in)
}

// Relaxed collapses provably redundant runs only.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if nonTerminalReporter(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := jsutil.Streams(in)
	outLines, c1 := collapse.Runs(collapse.SplitLines(stdout))
	errLines, c2 := collapse.Runs(collapse.SplitLines(stderr))
	if !c1 && !c2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append(append([]string(nil), outLines...), errLines...)
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0}, nil
}

func nonTerminalReporter(argv []string) bool {
	for i, a := range argv {
		var v string
		switch {
		case a == "--reporter" && i+1 < len(argv):
			v = argv[i+1]
		case strings.HasPrefix(a, "--reporter="):
			v = strings.TrimPrefix(a, "--reporter=")
		default:
			continue
		}
		for _, r := range strings.Split(v, ",") {
			switch r {
			case "list", "line", "dot":
			default:
				return true
			}
		}
	}
	return false
}

// failure is one failing test, folded across its retry attempts.
type failure struct {
	title    string
	msg      []string
	frame    string
	attach   []string
	attempts int
	retryMsg []string // first error line of attempts that differ from the first
	hidden   int      // code-frame / boilerplate lines dropped
}

type stat struct {
	label, n, dur string
}

// Render is the aggressive tier over a captured run; exported so the npx and
// package-manager wrappers can delegate when they recognise the shape.
func Render(in format.Input) (format.Rendered, error) {
	if nonTerminalReporter(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))

	var (
		fails    []*failure
		byTitle  = map[string]*failure{}
		cur      *failure
		curBlock []string
		stats    []stat
		other    []string
		sawFail  bool
		failN    int
	)
	flush := func() {
		if cur == nil {
			return
		}
		f := cur
		msg, frame, attach, hidden := digest(curBlock, f.title)
		cur, curBlock = nil, nil
		if first, ok := byTitle[f.title]; ok {
			first.attempts++
			first.hidden += hidden
			if len(first.attach) == 0 {
				first.attach = attach
			}
			if len(msg) > 0 && (len(first.msg) == 0 || msg[0] != first.msg[0]) {
				first.retryMsg = append(first.retryMsg, msg[0])
			}
			return
		}
		f.msg, f.frame, f.attach, f.hidden = msg, frame, attach, hidden
		f.attempts = 1
		byTitle[f.title] = f
		fails = append(fails, f)
	}
	inStats := false
	for _, l := range lines {
		if m := failHeadRE.FindStringSubmatch(l); m != nil && !inStats {
			flush()
			sawFail = true
			cur = &failure{title: m[2]}
			continue
		}
		if m := retryRE.FindStringSubmatch(l); m != nil && cur != nil {
			// A retry attempt restarts the same test's block.
			t := cur.title
			flush()
			cur = &failure{title: t}
			continue
		}
		if m := statRE.FindStringSubmatch(l); m != nil {
			flush()
			inStats = true
			s := stat{label: m[2], n: m[1], dur: m[3]}
			stats = append(stats, s)
			if s.label == "failed" {
				failN, _ = strconv.Atoi(s.n)
			}
			continue
		}
		if inStats {
			// The summary's repeat list of failing test titles.
			continue
		}
		if cur != nil {
			curBlock = append(curBlock, l)
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" || runningRE.MatchString(l) || progressRE.MatchString(l) {
			continue
		}
		other = append(other, l)
	}
	flush()
	if len(stats) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(fails) < failN || (sawFail && len(fails) == 0) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if in.ExitCode != 0 && len(fails) == 0 {
		// Interrupted, global-setup failure, no tests...: keep the raw output.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return renderAll(stats, fails, other, len(stderr) > 0), nil
}

// digest turns one attempt's raw block into message lines, the failing
// source position, attachment paths and a count of dropped lines.
func digest(block []string, title string) (msg []string, frame string, attach []string, hidden int) {
	file := title
	if i := strings.Index(file, ":"); i >= 0 {
		file = file[:i]
	}
	if i := strings.LastIndex(file, " › "); i >= 0 {
		file = file[i+len(" › "):]
	}
	inCall := false
	callKept := 0
	for _, l := range block {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			inCall = false
			continue
		case progressRE.MatchString(l):
			// The line reporter interleaves "[3/4] file › title" progress.
			continue
		case codeLineRE.MatchString(l) || caretRE.MatchString(l):
			hidden++
			continue
		case ruleRE.MatchString(l):
			continue
		case showTraceRE.MatchString(l):
			hidden++
			continue
		}
		if m := atRE.FindStringSubmatch(l); m != nil {
			p := m[1]
			if i := strings.Index(p, "/"+file); file != "" && i >= 0 && strings.HasSuffix(p[i+1:], file) {
				p = p[i+1:]
			}
			if frame == "" {
				frame = p + ":" + m[2] + ":" + m[3]
			} else {
				hidden++
			}
			continue
		}
		if m := attachRE.FindStringSubmatch(l); m != nil {
			continue
		}
		if m := ctxRE.FindStringSubmatch(l); m != nil {
			attach = append(attach, m[1])
			continue
		}
		if pathLineRE.MatchString(l) {
			attach = append(attach, t)
			continue
		}
		if strings.HasPrefix(t, "Call log:") {
			inCall, callKept = true, 0
			msg = append(msg, "Call log:")
			continue
		}
		if inCall {
			if callKept >= callLogCap {
				hidden++
				continue
			}
			callKept++
		}
		n := 0
		for n < 4 && n < len(l) && l[n] == ' ' {
			n++
		}
		msg = append(msg, l[n:])
	}
	return msg, frame, attach, hidden
}

func renderAll(stats []stat, fails []*failure, other []string, fold bool) format.Rendered {
	var parts []string
	var dur string
	for _, s := range stats {
		parts = append(parts, s.n+" "+s.label)
		if s.dur != "" {
			dur = s.dur
		}
	}
	var b strings.Builder
	b.WriteString("playwright: " + strings.Join(parts, ", "))
	if dur != "" {
		b.WriteString(", " + dur)
	}
	elided := false
	hidden := 0
	for _, f := range fails {
		b.WriteString("\nFAIL " + f.title)
		if f.attempts > 1 {
			b.WriteString(" (" + strconv.Itoa(f.attempts) + " attempts)")
		}
		for _, l := range f.msg {
			b.WriteString("\n  " + l)
		}
		for _, r := range f.retryMsg {
			b.WriteString("\n  retry differed: " + r)
		}
		if f.frame != "" {
			b.WriteString("\n  at " + f.frame)
		}
		if len(f.attach) > 0 {
			b.WriteString("\n  files: " + joinPaths(f.attach))
		}
		hidden += f.hidden
	}
	if hidden > 0 {
		b.WriteString("\n…+" + strconv.Itoa(hidden) + " code-frame/call-log lines")
		elided = true
	}
	if len(other) > 0 {
		b.WriteString("\n…+" + strconv.Itoa(len(other)) + " other lines")
		elided = true
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: elided}
}

// joinPaths lists paths, factoring out a directory they all share.
func joinPaths(ps []string) string {
	if len(ps) < 2 {
		return strings.Join(ps, " ")
	}
	dir := ps[0][:strings.LastIndex(ps[0], "/")+1]
	for _, p := range ps[1:] {
		if !strings.HasPrefix(p, dir) {
			return strings.Join(ps, " ")
		}
	}
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p[len(dir):]
	}
	return dir + "{" + strings.Join(names, ",") + "}"
}
