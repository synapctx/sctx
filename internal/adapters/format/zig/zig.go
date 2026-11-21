// Package zig formats `zig build`, `zig test` and `zig build-exe/lib/obj`.
// The build-step tree's repeat after the Build Summary, the cache-path command
// lines, per-test "OK" rows and standard-library stack frames (with their
// echoed source and caret) are dropped; compiler errors, failing assertions,
// project stack frames and the Build Summary are kept.
package zig

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `zig` output. It holds no mutable state.
type Formatter struct{}

// New constructs the zig formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `zig` invocation; applicability is decided from argv.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "zig"} }

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	treeRE     = regexp.MustCompile(`^\s*\+- `)
	frameRE    = regexp.MustCompile(`^(\S+):\d+:\d+: 0x[0-9a-f]+ in `)
	caretRE    = regexp.MustCompile(`^\s*\^+~*\s*$`)
	testOKRE   = regexp.MustCompile(`^\d+/\d+ \S.*\.\.\. OK$`)
	cmdLineRE  = regexp.MustCompile(`^/\S+ .*(?:--cache-dir|--listen|--seed|^/\S*/test)`)
	cmdHeadRE  = regexp.MustCompile(`^error: (?:the following|while executing test .* the following) .*(?:command failed|:$)`)
	stepNameRE = regexp.MustCompile(`^[a-zA-Z][\w .-]*(?: transitive failure)?$`)
)

func applicable(argv []string) bool {
	sub := ""
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			switch a {
			case "--help", "-h", "--watch", "-fwatch", "--fuzz", "-ffuzz", "-fno-emit-bin":
				return false
			}
			continue
		}
		if sub == "" {
			sub = a
			switch a {
			case "build", "test", "build-exe", "build-lib", "build-obj":
				continue
			}
			return false
		}
		// Build steps and positional sources: `run` executes the user's program.
		if sub == "build" && (a == "run" || strings.HasPrefix(a, "run-")) {
			return false
		}
	}
	return sub != ""
}

// Aggressive drops the step tree, command lines, passing rows and std frames.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only passing rows and the repeated (transitive failure) tree.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !applicable(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("zig: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("zig: reading stderr: %w", err)
	}
	text := ansiRE.ReplaceAllString(string(rawOut)+string(rawErr), "")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped := filter(strings.Split(text, "\n"), aggressive)
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d lines (step tree, command lines, passing tests, std frames, source echoes)", dropped))
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, aggressive bool) (kept []string, dropped int) {
	afterSummary := false
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t\r")
		switch {
		case testOKRE.MatchString(l):
			dropped++
			continue
		case strings.HasPrefix(l, "Build Summary:"):
			afterSummary = true
			kept = append(kept, l)
			continue
		}
		// A step tree: a bare step name followed by "+- ..." lines.
		isTree := treeRE.MatchString(l) || (stepNameRE.MatchString(l) && i+1 < len(lines) && treeRE.MatchString(lines[i+1]))
		if isTree {
			// The block printed after "Build Summary" repeats the first one with
			// "transitive failure" markers; the first copy names the failing step.
			if afterSummary {
				dropped++
				continue
			}
		}
		if !aggressive {
			kept = append(kept, l)
			continue
		}
		if m := frameRE.FindStringSubmatch(l); m != nil {
			// Drop the echoed source line and caret under every frame; drop the
			// whole frame when it is inside the standard library.
			skip := 0
			if i+1 < len(lines) && !frameRE.MatchString(lines[i+1]) && !strings.HasPrefix(lines[i+1], "error:") {
				skip = 1
				if i+2 < len(lines) && caretRE.MatchString(lines[i+2]) {
					skip = 2
				}
			}
			if strings.Contains(m[1], "/lib/zig/std/") || strings.HasPrefix(m[1], "std/") {
				dropped += 1 + skip
			} else {
				kept = append(kept, l)
				dropped += skip
			}
			i += skip
			continue
		}
		if cmdLineRE.MatchString(l) && i > 0 && cmdHeadRE.MatchString(strings.TrimRight(lines[i-1], " ")) {
			dropped++
			continue
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
