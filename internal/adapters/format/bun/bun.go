// Package bun implements format.Formatter for `bun test`. bun prints only the
// failing tests in a non-terminal run, but each one carries a source code frame
// and an absolute stack path; the render keeps the error message, the
// expected/received diff, the failing position (relative to the test file) and
// the test name, and counts the code-frame lines behind a marker.
package bun

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	headerRE  = regexp.MustCompile(`^bun test v\S+`)
	fileRE    = regexp.MustCompile(`^(\S+):$`)
	frameRE   = regexp.MustCompile(`^\s*\d+ \|`)
	caretRE   = regexp.MustCompile(`^\s+\^\s*$`)
	atRE      = regexp.MustCompile(`^\s+at (?:.*? \()?(\S+?):(\d+):(\d+)\)?$`)
	failRE    = regexp.MustCompile(`^\(fail\) (.+?)(?: \[([^\]]+)\])?$`)
	okRE      = regexp.MustCompile(`^\((pass|skip|todo)\) `)
	countRE   = regexp.MustCompile(`^\s*(\d+) (pass|fail|skip|todo|expect\(\) calls)$`)
	ranRE     = regexp.MustCompile(`^Ran (\d+) tests? across (\d+) files?\. \[([^\]]+)\]$`)
	noteRE    = regexp.MustCompile(`^\s+\^ (.+)$`)
	unhandled = regexp.MustCompile(`^# Unhandled error|^error: Unhandled|^Unhandled error`)
)

// Formatter renders `bun test` output.
type Formatter struct{}

// New constructs the bun test formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims `bun test` only.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "bun", Subcommands: []string{"test"}}
}

// Aggressive renders the summary-and-failures view.
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

type failure struct {
	name, dur, file, frame string
	msg                    []string
	notes                  []string
	hidden                 int
}

// Render is the aggressive tier, exported for package-manager delegation.
func Render(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv {
		if strings.HasPrefix(a, "--reporter") || a == "--watch" || a == "--hot" {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr := jsutil.Streams(in)
	lines := jsutil.Lines(append(append([]byte(nil), stdout...), append([]byte("\n"), stderr...)...))

	var (
		fails   []*failure
		pending []string
		file    string
		counts  []string
		ran     string
		sawBun  bool
		sawRan  bool
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case headerRE.MatchString(t):
			sawBun = true
			continue
		case unhandled.MatchString(t):
			return format.Rendered{}, format.ErrTierInapplicable
		case okRE.MatchString(t):
			continue
		}
		if m := failRE.FindStringSubmatch(l); m != nil {
			f := &failure{name: m[1], dur: m[2], file: file}
			f.msg, f.frame, f.hidden = digest(pending, file)
			pending = nil
			fails = append(fails, f)
			continue
		}
		if m := noteRE.FindStringSubmatch(l); m != nil && len(fails) > 0 && len(pending) == 0 {
			last := fails[len(fails)-1]
			last.notes = append(last.notes, m[1])
			continue
		}
		if m := countRE.FindStringSubmatch(l); m != nil {
			if m[2] != "expect() calls" {
				counts = append(counts, m[1]+" "+m[2])
			}
			continue
		}
		if m := ranRE.FindStringSubmatch(l); m != nil {
			sawRan = true
			ran = m[1] + " tests, " + m[2] + " " + jsutil.Plural(atoi(m[2]), "file", "files") + ", " + m[3]
			continue
		}
		if t == "" {
			continue
		}
		if m := fileRE.FindStringSubmatch(l); m != nil && !frameRE.MatchString(l) && len(pending) == 0 {
			file = m[1]
			continue
		}
		pending = append(pending, l)
	}
	if !sawBun || !sawRan {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(pending) > 0 || (in.ExitCode != 0 && len(fails) == 0) {
		// Output outside any (fail) block: not ours to summarise.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	b.WriteString("bun test: " + strings.Join(counts, ", ") + " (" + ran + ")")
	hidden := 0
	for _, f := range fails {
		b.WriteString("\nFAIL " + f.name)
		if f.dur != "" {
			b.WriteString(" [" + f.dur + "]")
		}
		if f.frame != "" {
			b.WriteString(" — " + f.frame)
		} else if f.file != "" {
			b.WriteString(" — " + f.file)
		}
		for _, m := range f.msg {
			b.WriteString("\n  " + m)
		}
		for _, n := range f.notes {
			b.WriteString("\n  ^ " + n)
		}
		hidden += f.hidden
	}
	if hidden > 0 {
		b.WriteString("\n…+" + strconv.Itoa(hidden) + " code-frame/stack lines")
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(stderr) > 0, Elided: hidden > 0}, nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// digest splits one failure's raw lines into message lines, the failing
// position relative to the test file, and a count of dropped frame lines.
func digest(raw []string, file string) (msg []string, frame string, hidden int) {
	for _, l := range raw {
		if frameRE.MatchString(l) || caretRE.MatchString(l) {
			hidden++
			continue
		}
		if m := atRE.FindStringSubmatch(l); m != nil {
			if frame == "" && jsutil.IsUserPath(m[1]) {
				p := m[1]
				if file != "" {
					if i := strings.Index(p, "/"+file); i >= 0 && strings.HasSuffix(p, file) {
						p = file
					}
				}
				frame = p + ":" + m[2] + ":" + m[3]
			} else {
				hidden++
			}
			continue
		}
		msg = append(msg, strings.TrimRight(l, " "))
	}
	return msg, frame, hidden
}
