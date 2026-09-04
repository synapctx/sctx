// This file delegates the region of `make`'s output that came from a
// recognisable inner tool — `go test`/`go vet`/`go build`, or `golangci-lint
// run` — to that tool's own formatter, reusing its parser rather than
// forking it. `make` alone was measured at 734 runs and 1.4% saved: 343 runs
// rendered "clean" (no dir-noise or repeated-echo collapse) yet barely
// shrank, because the inner `go test`/`go vet`/`golangci-lint` stream was
// never handed to those formatters. This closes that gap for the common
// case where the Makefile recipe is a bare invocation (`go test ./...`,
// `golangci-lint run`), optionally behind a `cd DIR &&`. A recipe that
// pipes, chains, or wraps the tool declines delegation for that block —
// only make's own collapseLines then applies to it, same as before this
// file existed.
package makefmt

import (
	"context"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/golangcilint"
	"github.com/synapctx/sctx/internal/adapters/format/gotest"
	"github.com/synapctx/sctx/internal/domain/format"
)

// recipeEcho matches the recipe-echo line make prints before running a
// recognised inner tool (GNU make echoes the recipe verbatim unless prefixed
// with `@`), optionally preceded by `cd DIR &&` (a common pattern for
// multi-module Makefiles). It requires the WHOLE line to be the bare
// invocation — no trailing pipe, chain, or redirect — because a recipe like
// `go test ./... | tee log.txt` hands the FORMATTER's output a stream that
// is no longer go test's own (tee's copy, possibly reordered or filtered by
// whatever comes after), and delegating to gotest would risk misreading it.
// Trailing flags and package paths on the bare form are fine; the inner
// formatter decides on its own whether it can parse what follows.
var recipeEcho = regexp.MustCompile(`^(?:cd\s+\S+\s*&&\s*)?(go\s+(test|vet|build)\b|golangci-lint\s+run\b)[^|;&<>` + "`" + `]*$`)

// makeErrorBanner matches make's own failure summary line, e.g.
// "make: *** [Makefile:10: test] Error 1" or "make[1]: *** [...] Error 2".
// It always ends a delegated block: it is make's line, not the inner tool's.
var makeErrorBanner = regexp.MustCompile(`^make(?:\[\d+\])?: \*\*\*`)

// innerKind names the inner tool a recipe-echo line identified.
type innerKind int

const (
	innerNone innerKind = iota
	innerGoTest
	innerGoVet
	innerGoBuild
	innerGolangci
)

// classifyRecipe reports which inner tool, if any, line is a recipe echo for.
func classifyRecipe(line string) innerKind {
	m := recipeEcho.FindStringSubmatch(line)
	if m == nil {
		return innerNone
	}
	if strings.Contains(m[0], "golangci-lint") {
		return innerGolangci
	}
	switch m[2] {
	case "test":
		return innerGoTest
	case "vet":
		return innerGoVet
	case "build":
		return innerGoBuild
	default:
		return innerNone
	}
}

// segment is either a contiguous run of make's OWN lines (candidate for
// collapseLines) or an already-rendered block from a delegated inner tool.
type segment struct {
	delegated bool
	lines     []string
	body      string
}

// splitDelegatableSegments walks lines, recognising a recipe-echo line
// followed by that tool's own output (up to the next recipe echo, directory
// marker, make error banner, or end of input) as a delegatable block. The
// echo line itself always stays with the surrounding make-owned segment —
// it is make's own recipe display, not the tool's output — so a decline
// here changes nothing about what collapseLines already did with it.
func splitDelegatableSegments(lines []string, exitCode int) []segment {
	var segs []segment
	var raw []string
	flushRaw := func() {
		if len(raw) > 0 {
			segs = append(segs, segment{lines: raw})
			raw = nil
		}
	}

	i := 0
	for i < len(lines) {
		kind := classifyRecipe(lines[i])
		if kind == innerNone {
			raw = append(raw, lines[i])
			i++
			continue
		}

		raw = append(raw, lines[i]) // the echo line itself: always make-owned
		i++

		start := i
		for i < len(lines) {
			if classifyRecipe(lines[i]) != innerNone {
				break
			}
			if _, ok := dirLineMatch(lines[i]); ok {
				break
			}
			if makeErrorBanner.MatchString(lines[i]) {
				break
			}
			i++
		}
		block := lines[start:i]

		if body, ok := delegateBlock(kind, block, exitCode); ok {
			flushRaw()
			segs = append(segs, segment{delegated: true, body: body})
		} else {
			raw = append(raw, block...)
		}
	}
	flushRaw()
	return segs
}

// delegateBlock hands block's lines to the inner tool's own formatter,
// trying the aggressive tier then the relaxed tier, mirroring the tier
// chain's own degrade order. ok is false when the block is empty, the
// formatter declines at both tiers, or the render is not actually smaller —
// the caller then keeps the block as make-owned lines, unchanged from
// before delegation existed.
func delegateBlock(kind innerKind, block []string, exitCode int) (string, bool) {
	if len(block) == 0 {
		return "", false
	}
	text := strings.Join(block, "\n")

	var fm format.Formatter
	var argv []string
	var command string
	switch kind {
	case innerGoTest:
		fm, argv, command = gotest.New(), []string{"go", "test"}, "go test"
	case innerGoVet:
		fm, argv, command = gotest.New(), []string{"go", "vet"}, "go vet"
	case innerGoBuild:
		fm, argv, command = gotest.New(), []string{"go", "build"}, "go build"
	case innerGolangci:
		fm, argv, command = golangcilint.New(), []string{"golangci-lint", "run"}, "golangci-lint run"
	default:
		return "", false
	}

	newInput := func() format.Input {
		return format.Input{
			Argv: argv, Command: command,
			Stdout:   strings.NewReader(text),
			ExitCode: exitCode,
		}
	}

	out, err := fm.Aggressive(context.Background(), newInput())
	if err != nil {
		out, err = fm.Relaxed(context.Background(), newInput())
	}
	if err != nil || len(out.Body) == 0 || len(out.Body) >= len(text) {
		return "", false
	}
	return string(out.Body), true
}
