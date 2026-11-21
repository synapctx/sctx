// Package perl formats `prove` (Test::Harness / TAP) output. Passing TAP rows,
// plan lines, passing-file rows and blank lines are dropped; `not ok` rows,
// the "#   Failed test" diagnostics with their got/expected detail, die
// messages and the harness's Test Summary Report are kept.
package perl

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `prove` output. It holds no mutable state.
type Formatter struct{}

// New constructs the prove formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `prove` invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "prove"} }

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	planRE     = regexp.MustCompile(`^1\.\.\d+$`)
	okRowRE    = regexp.MustCompile(`^ok \d+(?: - .*)?$`)
	fileOKRE   = regexp.MustCompile(`^\S+ \.+ ok$`)
	fileHeadRE = regexp.MustCompile(`^\S+ \.+ ?$`)
	failedNRE  = regexp.MustCompile(`^Failed \d+/\d+ subtests`)
	looksRE    = regexp.MustCompile(`^# Looks like you failed \d+ tests? of \d+\.$`)
)

func applicable(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "--formatter", "--harness", "--help", "-h", "-?", "--man", "--version", "-V", "--dry", "--archive", "--merge-nothing":
			return false
		}
		if strings.HasPrefix(a, "--formatter=") || strings.HasPrefix(a, "--harness=") || strings.HasPrefix(a, "--archive=") {
			return false
		}
	}
	return true
}

// Aggressive drops passing rows and duplicated failure restatements.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only blank lines, plan lines and passing TAP rows.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !applicable(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("perl: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("perl: reading stderr: %w", err)
	}
	// prove prints diagnostics on stderr ahead of the per-file status row on
	// stdout; stderr first keeps "#   Failed test" next to the file it belongs to.
	text := ansiRE.ReplaceAllString(string(rawErr)+string(rawOut), "")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped := filter(strings.Split(text, "\n"), aggressive)
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d lines (passing rows, plans, blanks, restated failure counts)", dropped))
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, aggressive bool) (kept []string, dropped int) {
	head := "" // file header held until a kept row follows it
	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t\r")
		switch {
		case l == "", planRE.MatchString(l):
			dropped++
			continue
		case okRowRE.MatchString(l):
			dropped++
			continue
		}
		if !aggressive {
			kept = append(kept, l)
			continue
		}
		switch {
		case fileOKRE.MatchString(l), failedNRE.MatchString(l), looksRE.MatchString(l):
			dropped++
			continue
		case l == "ok" && head != "":
			head = ""
			dropped += 2
			continue
		case fileHeadRE.MatchString(raw):
			if head != "" {
				// A header with no verdict line of its own: keep what we held.
				kept = append(kept, head)
			}
			head = strings.TrimRight(raw, " ")
			continue
		}
		if head != "" {
			kept = append(kept, head)
			head = ""
		}
		kept = append(kept, l)
	}
	if head != "" {
		kept = append(kept, head)
	}
	return kept, dropped
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
