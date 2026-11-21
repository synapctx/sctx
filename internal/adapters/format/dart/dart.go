// Package dart formats `dart analyze`, `dart test` and `dart pub get`. The test
// runner redraws one status row with carriage returns for every event; only
// the failing rows, their expected/actual detail and the final verdict are
// kept. analyze keeps every finding's location, severity and rule but drops
// the trailing "Try ..." advice; pub keeps its summary and drops the per-
// package resolution list.
package dart

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `dart` output. It holds no mutable state.
type Formatter struct{}

// New constructs the dart formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `dart` invocation; the subcommand is dispatched inside.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "dart"} }

var (
	ansiRE      = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	progressRE  = regexp.MustCompile(`^\d\d:\d\d \+\d+(?: [-~]\d+)*: `)
	verdictRE   = regexp.MustCompile(`\[E\]$|Some tests failed|All tests passed|No tests ran|\[F\]$`)
	frameRE     = regexp.MustCompile(`^\s+(?:package:(?:matcher|test|test_api|test_core|async|stack_trace)|dart:\S+)\s+\S`)
	findingRE   = regexp.MustCompile(`^\s*(?:error|warning|info) - \S+:\d+:\d+ - `)
	pkgAddedRE  = regexp.MustCompile(`^[+<>~] \S+ \S+(?: .*)?$`)
	chainNoteRE = regexp.MustCompile(`^(?:Consider enabling the flag chain-stack-traces|For example, 'dart test --chain-stack-traces')`)
)

type kind int

const (
	kNone kind = iota
	kAnalyze
	kTest
	kPub
)

func classify(argv []string) kind {
	var words []string
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			switch {
			case a == "-r" || strings.HasPrefix(a, "--reporter") || strings.HasPrefix(a, "--file-reporter") ||
				strings.HasPrefix(a, "--format=json") || a == "--json" || a == "--machine" || a == "--coverage" || strings.HasPrefix(a, "--coverage"):
				return kNone
			}
			continue
		}
		words = append(words, a)
		if len(words) == 2 {
			break
		}
	}
	if len(words) == 0 {
		return kNone
	}
	switch words[0] {
	case "analyze":
		return kAnalyze
	case "test":
		return kTest
	case "pub":
		if len(words) == 2 && (words[1] == "get" || words[1] == "upgrade" || words[1] == "downgrade") {
			return kPub
		}
	}
	return kNone
}

// Aggressive keeps findings, failures and verdicts only.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only redrawn progress rows (test) and blanks.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	k := classify(in.Argv)
	if k == kNone {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("dart: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("dart: reading stderr: %w", err)
	}
	text := ansiRE.ReplaceAllString(string(rawOut)+string(rawErr), "")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped, hints := filter(strings.Split(text, "\n"), k, aggressive)
	if dropped == 0 && hints == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("…+%d lines (progress rows, blanks, framework frames, package list)", dropped))
	}
	if hints > 0 {
		kept = append(kept, fmt.Sprintf("…+%d \"Try ...\" hints", hints))
	}
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, k kind, aggressive bool) (kept []string, dropped, hints int) {
	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		switch {
		case strings.TrimSpace(l) == "":
			dropped++
			continue
		case k == kTest && progressRE.MatchString(l):
			if verdictRE.MatchString(l) {
				kept = append(kept, l)
			} else {
				dropped++
			}
			continue
		}
		if !aggressive {
			kept = append(kept, l)
			continue
		}
		switch k {
		case kTest:
			if frameRE.MatchString(l) || chainNoteRE.MatchString(l) {
				dropped++
				continue
			}
		case kAnalyze:
			if strings.HasPrefix(l, "Analyzing ") {
				dropped++
				continue
			}
			if findingRE.MatchString(l) {
				if i := strings.Index(l, " Try "); i >= 0 {
					if j := strings.LastIndex(l, " - "); j > i {
						l = l[:i] + l[j:]
						hints++
					}
				}
			}
		case kPub:
			if strings.HasPrefix(l, "Resolving dependencies") || strings.HasPrefix(l, "Downloading packages") || pkgAddedRE.MatchString(l) {
				dropped++
				continue
			}
		}
		kept = append(kept, l)
	}
	return kept, dropped, hints
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
