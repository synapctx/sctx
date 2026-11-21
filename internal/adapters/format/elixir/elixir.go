// Package elixir formats `mix` output for compile, test, format and deps
// tasks. Compile noise (dependency headers, "Compiling N files", "Generated
// app") and ExUnit progress rows are dropped; warnings and errors keep their
// message and "└─ path:line: Mod.fun/arity" location but lose the boxed
// source excerpt; test failures keep their assertion, code, left/right values
// and project stacktrace frames; format failures keep the file list and lose
// the diff bodies.
package elixir

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `mix` output. It holds no mutable state.
type Formatter struct{}

// New constructs the mix formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `mix` invocation; the task is dispatched inside.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "mix"} }

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	depHeadRE  = regexp.MustCompile(`^==> \S+$`)
	compilingR = regexp.MustCompile(`^(?:Compiling \d+ files? \(\.ex\)|Generated \S+ app)$`)
	progressRE = regexp.MustCompile(`^[.*]+$`)
	boxLineRE  = regexp.MustCompile(`^\s+(?:\d+ )?│`)
	diagRE     = regexp.MustCompile(`^\s{0,4}(?:warning|error): `)
	gettingRE  = regexp.MustCompile(`^\* (?:Getting|Updating|Unlocking) `)
	seedRE     = regexp.MustCompile(`^Running ExUnit with seed: \d+`)
)

// task returns the mix task named by argv, or "" when none or unsupported.
func task(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "compile", "test", "format", "deps.get", "deps.compile", "deps.update":
			return a
		}
		return ""
	}
	return ""
}

func machineOrStreaming(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "--trace", "--stale-trace", "--dry-run", "--stdin", "--no-compile-only-list":
			return true
		}
		if strings.HasPrefix(a, "--formatter") || a == "--" {
			return true
		}
	}
	return false
}

// Aggressive drops compile chatter, progress rows and excerpts.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only ANSI, blank lines and ExUnit progress rows.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	t := task(in.Argv)
	if t == "" || machineOrStreaming(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("elixir: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("elixir: reading stderr: %w", err)
	}
	// Compiler diagnostics go to stderr and precede the test run on stdout.
	text := ansiRE.ReplaceAllString(string(rawErr)+string(rawOut), "")
	var lines []string
	if text = strings.TrimRight(text, "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped := filter(lines, t, aggressive, in.ExitCode != 0)
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d lines (compile progress, source excerpts, diffs, blanks)", dropped))
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, task string, aggressive, failed bool) (kept []string, dropped int) {
	pendingHead, lastBox := "", ""
	inFormatDiff := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			dropped++
			continue
		case progressRE.MatchString(t):
			dropped++
			continue
		case seedRE.MatchString(t) && !failed:
			dropped++
			continue
		}
		if task == "format" {
			if strings.HasPrefix(t, "The following files are not formatted") {
				inFormatDiff = true
				kept = append(kept, l)
				continue
			}
			if inFormatDiff && aggressive {
				if strings.HasPrefix(t, "/") || strings.HasPrefix(t, "lib/") || strings.HasPrefix(t, "test/") || strings.HasPrefix(t, "config/") {
					kept = append(kept, t)
				} else {
					dropped++
				}
				continue
			}
		}
		if !aggressive {
			kept = append(kept, l)
			continue
		}
		switch {
		case depHeadRE.MatchString(l):
			pendingHead = l
			dropped++
			continue
		case compilingR.MatchString(l):
			dropped++
			continue
		case gettingRE.MatchString(l) && task != "deps.get":
			dropped++
			continue
		case boxLineRE.MatchString(l):
			// A boxed excerpt line is dropped unless it is a label ("└ unclosed
			// delimiter"); a label keeps the numbered source line it points at.
			if strings.Contains(l, "└ ") || strings.Contains(l, "┌ ") {
				if lastBox != "" {
					kept = append(kept, lastBox)
					dropped--
					lastBox = ""
				}
				kept = append(kept, l)
				continue
			}
			lastBox = l
			dropped++
			continue
		}
		lastBox = ""
		if pendingHead != "" && (diagRE.MatchString(l) || strings.HasPrefix(l, "==") || strings.HasPrefix(l, "**")) {
			kept = append(kept, pendingHead)
			dropped--
		}
		pendingHead = ""
		if strings.HasPrefix(l, "    ") && diagRE.MatchString(l) {
			l = strings.TrimLeft(l, " ")
		}
		kept = append(kept, l)
	}
	return kept, dropped
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
