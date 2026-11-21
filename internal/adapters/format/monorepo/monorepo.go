// Package monorepo implements format.Formatter for the two JavaScript
// monorepo task runners, turbo and nx. Both interleave every task's log with
// banners, cache chatter and per-task script echoes; a green task's log is
// rarely what a reader wants, a red task's log always is.
//
// The render keeps: one line per succeeded task (with its cache status), the
// FULL log of every failed task (minus the package manager's script echo and
// path-only error lines, which are counted), warnings, the run summary and the
// failed-task list. A succeeded task's log is counted behind an explicit
// marker, never silently dropped.
package monorepo

import (
	"context"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/domain/format"
)

// maxOK bounds how many succeeded task names are spelled out.
const maxOK = 15

// Formatter renders one runner's output.
type Formatter struct{ kind string }

// NewTurbo constructs the turbo formatter.
func NewTurbo() *Formatter { return &Formatter{kind: "turbo"} }

// NewNx constructs the nx formatter.
func NewNx() *Formatter { return &Formatter{kind: "nx"} }

// All returns the turbo and nx formatters.
func All() []format.Formatter { return []format.Formatter{NewTurbo(), NewNx()} }

// Descriptor claims every invocation of the runner's binary; Aggressive
// declines output that is not a task run (graph, ls, --dry, servers...).
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.kind} }

// Aggressive renders the per-task summary.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if f.kind == "turbo" {
		return RenderTurbo(in)
	}
	return RenderNx(in)
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

// task is one task's accumulated state, shared by both runners.
type task struct {
	name   string
	note   string // cache status
	failed bool
	logs   []string
	hidden int
}

type taskSet struct {
	order []*task
	by    map[string]*task
}

func newTaskSet() *taskSet { return &taskSet{by: map[string]*task{}} }

func (s *taskSet) get(name string) *task {
	if t, ok := s.by[name]; ok {
		return t
	}
	t := &task{name: name}
	s.by[name] = t
	s.order = append(s.order, t)
	return t
}

func (s *taskSet) render(b *strings.Builder) (hiddenTotal int) {
	var ok []string
	for _, t := range s.order {
		if t.failed {
			continue
		}
		n := t.name
		if t.note != "" {
			n += " [" + t.note + "]"
		}
		ok = append(ok, n)
		hiddenTotal += len(t.logs) + t.hidden
	}
	if len(ok) > 0 {
		b.WriteString("\n✔ " + strconv.Itoa(len(ok)) + " ok: ")
		if len(ok) > maxOK {
			b.WriteString(strings.Join(ok[:maxOK], ", ") + ", …+" + strconv.Itoa(len(ok)-maxOK) + " more")
		} else {
			b.WriteString(strings.Join(ok, ", "))
		}
	}
	for _, t := range s.order {
		if !t.failed {
			continue
		}
		b.WriteString("\n✖ " + t.name)
		if t.note != "" {
			b.WriteString(" [" + t.note + "]")
		}
		for _, l := range t.logs {
			b.WriteString("\n  " + l)
		}
		hiddenTotal += t.hidden
	}
	return hiddenTotal
}

// scriptEcho reports the package manager's own "> pkg@1.0.0 script" /
// "> command" banner lines.
func scriptEcho(s string) bool {
	return strings.HasPrefix(s, "> ")
}

// pathOnlyNpmError reports npm's path/workspace/location/command echo lines
// that follow a failed lifecycle script; the "code" and "Lifecycle script"
// lines are the ones that carry the failure.
func pathOnlyNpmError(s string) bool {
	for _, p := range []string{"npm error path ", "npm error workspace ", "npm error location ", "npm error command "} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
