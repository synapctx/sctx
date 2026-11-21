// Package precommit implements a format.Formatter for `pre-commit run`.
// Passed and skipped hooks collapse to counts, every failed hook keeps its id,
// exit code, "files were modified" flag and its own output (a shellcheck
// report inside a hook is condensed by the shellcheck formatter), and
// environment-install [INFO] progress lines are dropped and counted.
package precommit

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/lintkit"
	"github.com/synapctx/sctx/internal/adapters/format/shellcheck"
	"github.com/synapctx/sctx/internal/domain/format"
)

// maxHookLines bounds one hook's own output; the remainder is counted.
const maxHookLines = 100

var (
	statusRE     = regexp.MustCompile(`^(.+?)\.{3,}(?:\(([^)]*)\))?(Passed|Failed|Skipped)$`)
	shellcheckRE = regexp.MustCompile(`^In .+ line \d+:$`)
)

// Formatter renders pre-commit output.
type Formatter struct{}

// New constructs a pre-commit Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every pre-commit invocation; only `run` is condensed.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "pre-commit"} }

func isRun(argv []string) bool {
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "run"
	}
	return false
}

type hook struct {
	name, status, note string
	meta               []string // "- hook id: x", "- exit code: 1", ...
	body               []string
}

// Aggressive condenses a `pre-commit run`. A failing run exits 1 and that is
// the normal case, so the report shape decides applicability.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if len(in.Argv) < 2 || !isRun(in.Argv) || len(lintkit.ReadAll(in.Stderr)) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := lintkit.ReadAll(in.Stdout)
	var (
		hooks []*hook
		cur   *hook
		info  int
		other []string // lines before the first hook that are not [INFO]
	)
	for _, l := range lintkit.Lines(raw) {
		l = lintkit.StripANSI(l)
		if m := statusRE.FindStringSubmatch(l); m != nil {
			cur = &hook{name: m[1], status: m[3], note: m[2]}
			hooks = append(hooks, cur)
			continue
		}
		if cur == nil {
			if strings.HasPrefix(l, "[INFO] ") {
				info++
			} else if strings.TrimSpace(l) != "" {
				other = append(other, l)
			}
			continue
		}
		if len(cur.body) == 0 && isMeta(l) {
			cur.meta = append(cur.meta, l[2:])
			continue
		}
		cur.body = append(cur.body, l)
	}
	if len(hooks) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	passed, skipped, failed := 0, 0, 0
	var detail []string
	elided := info > 0
	for _, h := range hooks {
		h.body = trimBlank(h.body)
		switch {
		case h.status == "Failed":
			failed++
		case len(h.body) == 0 && h.status == "Passed":
			passed++
			continue
		case len(h.body) == 0 && h.status == "Skipped":
			skipped++
			continue
		case h.status == "Passed":
			passed++
		default:
			skipped++
		}
		detail = append(detail, renderHook(h, &elided)...)
	}
	var head strings.Builder
	fmt.Fprintf(&head, "pre-commit: %d failed", failed)
	if passed > 0 {
		fmt.Fprintf(&head, ", ×%d passed", passed)
	}
	if skipped > 0 {
		fmt.Fprintf(&head, ", ×%d skipped", skipped)
	}
	out := append(other, head.String())
	out = append(out, detail...)
	if info > 0 {
		out = append(out, fmt.Sprintf("…+%d [INFO] lines (environment setup)", info))
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d hooks", len(hooks)), Elided: elided}, nil
}

func isMeta(l string) bool {
	for _, p := range []string{"- hook id:", "- exit code:", "- duration:", "- files were modified by this hook"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func renderHook(h *hook, elided *bool) []string {
	var id, exit, dur string
	modified := false
	for _, m := range h.meta {
		switch {
		case strings.HasPrefix(m, "hook id: "):
			id = strings.TrimPrefix(m, "hook id: ")
		case strings.HasPrefix(m, "exit code: "):
			exit = strings.TrimPrefix(m, "exit code: ")
		case strings.HasPrefix(m, "duration: "):
			dur = strings.TrimPrefix(m, "duration: ")
		case strings.HasPrefix(m, "files were modified"):
			modified = true
		}
	}
	title := strings.ToUpper(h.status)
	if id != "" {
		title += " " + id
	} else {
		title += " " + h.name
	}
	var tags []string
	if exit != "" {
		tags = append(tags, "exit "+exit)
	}
	if modified {
		tags = append(tags, "files were modified by this hook")
	}
	if dur != "" {
		tags = append(tags, dur)
	}
	if h.note != "" {
		tags = append(tags, h.note)
	}
	if len(tags) > 0 {
		title += " [" + strings.Join(tags, "; ") + "]"
	}
	out := []string{title}
	body := h.body
	if len(body) > 0 && shellcheckRE.MatchString(strings.TrimSpace(body[0])) {
		if s, ok := shellcheck.Condense(body); ok {
			for _, l := range strings.Split(s, "\n") {
				out = append(out, "  "+l)
			}
			*elided = true
			return out
		}
	}
	if len(body) > maxHookLines {
		out = append(out, indent(body[:maxHookLines])...)
		out = append(out, fmt.Sprintf("  …+%d more lines", len(body)-maxHookLines))
		*elided = true
		return out
	}
	return append(out, indent(body)...)
}

func indent(ls []string) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = "  " + l
	}
	return out
}

func trimBlank(ls []string) []string {
	for len(ls) > 0 && strings.TrimSpace(ls[0]) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// Relaxed declines: anything the aggressive tier cannot model is kept as is.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
