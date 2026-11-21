// Package pyinstall implements format.Formatters for the Python dependency
// installers and resolvers: `uv sync|add|remove|lock|pip install|pip
// uninstall`, `poetry install|add|remove|update|lock`, `pdm
// install|add|remove|update|sync|lock` and `pip-compile`.
//
// Their reports share one shape: progress chatter (downloads, builds, the
// interpreter banner), one line per package operation, a summary, and — on
// failure — an error block that can carry a full Python traceback. The
// rendering drops the progress (counted), folds the per-package lines into one
// "+ a==1 b==2" line per operation kind, strips timings from the summary and
// collapses traceback frames to the exception line, keeping every error and
// warning verbatim.
package pyinstall

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxNames caps the package names kept on one operation line.
const maxNames = 40

// maxBlock caps a run of kept lines that is not a recognised traceback
// (a build backend's own output, for instance): head and tail are kept.
const (
	maxKept  = 40
	keepHead = 12
	keepTail = 18
)

type tool int

const (
	toolUV tool = iota
	toolPoetry
	toolPDM
	toolPipCompile
)

// Formatter renders one installer command (a program plus subcommand prefix).
type Formatter struct {
	match format.Match
	tool  tool
}

func mk(cmd string, t tool, subs ...string) *Formatter {
	return &Formatter{match: format.Match{Command: cmd, Subcommands: subs}, tool: t}
}

// All returns every formatter this package provides, for registration.
func All() []*Formatter {
	var fs []*Formatter
	for _, s := range [][]string{{"sync"}, {"add"}, {"remove"}, {"lock"}, {"pip", "install"}, {"pip", "uninstall"}} {
		fs = append(fs, mk("uv", toolUV, s...))
	}
	for _, s := range []string{"install", "add", "remove", "update", "lock"} {
		fs = append(fs, mk("poetry", toolPoetry, s))
	}
	for _, s := range []string{"install", "add", "remove", "update", "sync", "lock"} {
		fs = append(fs, mk("pdm", toolPDM, s))
	}
	fs = append(fs, mk("pip-compile", toolPipCompile))
	return fs
}

// Descriptor claims the program and subcommand this formatter was built for.
func (f *Formatter) Descriptor() format.Match { return f.match }

// Aggressive renders the compact report.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in)
}

// Relaxed shares the aggressive rendering; every rule is lossless apart from
// counted progress lines.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in)
}

func readAll(r io.Reader) string {
	if r == nil {
		return ""
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

var (
	// uvOpRe: " + name==1.0", " - name==1.0", " ~ name==1.0".
	uvOpRe = regexp.MustCompile(`^ ([+\-~]) (\S+==\S+)$`)
	// poetryOpRe: "  - Installing name (1.0)", "  - Updating name (1 -> 2)".
	poetryOpRe = regexp.MustCompile(`^\s+[-•] (Installing|Updating|Removing) (\S+) \((.+)\)$`)
	// pdmOpRe: "  ✔ Install name 1.0 successful", "✔ Update name 1 -> 2 successful".
	pdmOpRe = regexp.MustCompile(`^\s*✔ (Install|Update|Remove) (\S+) (.+?) successful$`)
	// pinRe: a pinned line in pip-compile's echo of requirements.txt.
	pinRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-\[\]]*==\S+`)
	// uvSummaryRe: "Resolved 15 packages in 552ms" → timing is noise.
	uvSummaryRe = regexp.MustCompile(`^((?:Resolved|Prepared|Installed|Uninstalled|Audited|Checked|Built) \d+ packages?) in [\d.]+m?s$`)
	// progressRe: lines that only narrate progress.
	progressRe = regexp.MustCompile(`^\s*(Downloading |Downloaded |Building |Built \S+$|Using (CPython|Python) \S+ (interpreter|environment) at|Using virtualenv|Creating virtual ?env|⠋|⠙|⠹|⠸|⠼|⠴|⠦|⠧|⠇|⠏)`)
	// setuptoolsBlockOpen/Close bracket pdm's editable-wheel deprecation block.
	tracebackRe = regexp.MustCompile(`^Traceback \(most recent call last\):$`)
)

// indentOf counts leading spaces.
func indentOf(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

type op struct {
	sign, text string
}

func (f *Formatter) render(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv[1:] {
		switch a {
		case "-v", "-vv", "-vvv", "--verbose", "--debug", "--help", "-h", "--version":
			// Verbose output is the user's explicit request for the detail.
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stderr := readAll(in.Stderr)
	stdout := readAll(in.Stdout)
	text := strings.TrimRight(stderr+stdout, "\r\n")
	if strings.TrimSpace(text) == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := strings.Split(text, "\n")

	var kept []string
	var ops []op
	dropped := 0
	elided := false
	flushOps := func() {
		if len(ops) == 0 {
			return
		}
		kept = append(kept, renderOps(ops))
		ops = nil
	}
	pushOp := func(o op) {
		if len(ops) > 0 && ops[len(ops)-1].sign != o.sign {
			flushOps()
		}
		ops = append(ops, o)
	}

	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t\r")
		if f.tool == toolPipCompile {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "#") {
				dropped++
				continue
			}
			if pinRe.MatchString(t) && !strings.HasPrefix(l, " ") {
				pushOp(op{"=", t})
				continue
			}
		}
		switch {
		case strings.TrimSpace(l) == "":
			continue
		case f.tool == toolUV && uvOpRe.MatchString(l):
			m := uvOpRe.FindStringSubmatch(l)
			pushOp(op{m[1], m[2]})
			continue
		case f.tool == toolPoetry && poetryOpRe.MatchString(l):
			m := poetryOpRe.FindStringSubmatch(l)
			switch m[1] {
			case "Installing":
				pushOp(op{"+", m[2] + "==" + m[3]})
			case "Removing":
				pushOp(op{"-", m[2] + "==" + m[3]})
			default:
				pushOp(op{"~", m[2] + " (" + m[3] + ")"})
			}
			continue
		case f.tool == toolPDM && pdmOpRe.MatchString(l):
			m := pdmOpRe.FindStringSubmatch(l)
			sign := map[string]string{"Install": "+", "Remove": "-", "Update": "~"}[m[1]]
			pushOp(op{sign, m[2] + " " + m[3]})
			continue
		case f.tool == toolPDM && strings.TrimSpace(l) == "!!":
			// setuptools' editable-install deprecation banner: "!!" ... "!!"
			// followed by the "with strategy, ..." warning source line.
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) != "!!" {
				j++
			}
			if j < len(lines) {
				if j+1 < len(lines) && strings.Contains(lines[j+1], "with strategy") {
					j++
				}
				dropped += j - i + 1
				i = j
				continue
			}
		case progressRe.MatchString(l):
			dropped++
			continue
		case tracebackRe.MatchString(strings.TrimSpace(l)):
			flushOps()
			base := indentOf(l)
			j := i + 1
			for j < len(lines) && (strings.TrimSpace(lines[j]) == "" || indentOf(lines[j]) > base) {
				j++
			}
			// lines[j] is the exception line that ends this traceback.
			if j < len(lines) {
				kept = append(kept, strings.Repeat(" ", base)+fmt.Sprintf("Traceback: …+%d frame lines", j-i-1))
				kept = append(kept, strings.TrimRight(lines[j], " \t\r"))
				elided = true
				i = j
				continue
			}
		}
		if m := uvSummaryRe.FindStringSubmatch(l); m != nil {
			l = m[1]
		}
		flushOps()
		kept = append(kept, l)
	}
	flushOps()

	if len(kept) > maxKept {
		more := len(kept) - keepHead - keepTail
		head, tail := kept[:keepHead], kept[len(kept)-keepTail:]
		kept = append(append(append([]string{}, head...), fmt.Sprintf("…+%d lines", more)), tail...)
		elided = true
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("[%d progress/comment lines omitted]", dropped))
		elided = true
	}
	if len(kept) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(kept, "\n")), FoldStderr: stderr != "", Elided: elided}, nil
}

// renderOps folds consecutive operations of one kind into one line.
func renderOps(ops []op) string {
	names := make([]string, 0, len(ops))
	for _, o := range ops {
		names = append(names, o.text)
	}
	more := 0
	sep := " "
	if ops[0].sign == "~" {
		sep = ", "
	}
	if len(names) > maxNames {
		more = len(names) - maxNames
		names = names[:maxNames]
	}
	s := strings.Join(names, sep)
	if ops[0].sign != "=" {
		s = ops[0].sign + " " + s
	}
	if more > 0 {
		s += fmt.Sprintf(" …+%d more", more)
	}
	return s
}
