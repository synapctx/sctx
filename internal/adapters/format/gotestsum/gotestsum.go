// Package gotestsum formats output from `gotestsum`, the test runner that
// wraps `go test -json`. Whatever --format is chosen, gotestsum ends with a
// summary (=== Skipped, === Failed, === Errors, DONE ...) that carries every
// failure's output once; the live stream before it is a per-test or
// per-package progress feed that mostly repeats that summary. The aggressive
// tier keeps the summary (toolchain stack frames collapsed) and counts the
// live stream away; it declines when there is no failure summary to rely on.
package gotestsum

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxSkips is how many skipped tests are listed before the rest are counted.
const maxSkips = 5

var (
	sectionRE = regexp.MustCompile(`^=== (Skipped|Failed|Errors)$`)
	blockRE   = regexp.MustCompile(`^=== (FAIL|SKIP): (\S+) ?(.*?) ?\((\d+(?:\.\d+)?s|unknown)\)$`)
	doneRE    = regexp.MustCompile(`^DONE `)
	liveNoise = regexp.MustCompile(`^(✓|∅|↷|PASS |SKIP |EMPTY |=== (RUN|PAUSE|CONT|NAME) |--- (PASS|SKIP)|ok  \t|\?   \t)`)
)

// Formatter renders gotestsum output.
type Formatter struct{}

// New constructs a gotestsum Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every gotestsum invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "gotestsum"} }

// declined reports invocations whose output is not a test run: the `tool`
// subcommands, help/version, and formats that emit CI annotations.
func declined(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return false
		}
		if k, v, ok := strings.Cut(a, "="); ok {
			a = k
			if a == "--format" && strings.HasPrefix(v, "github-actions") {
				return true
			}
		}
		switch a {
		case "--version", "-h", "--help", "tool", "--watch", "-w":
			return true
		case "--format", "-f":
			if i+1 < len(argv) && strings.HasPrefix(argv[i+1], "github-actions") {
				return true
			}
		}
	}
	return false
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func readStreams(in format.Input) (stdout, stderr []string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return nil, nil, fmt.Errorf("gotestsum: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return nil, nil, fmt.Errorf("gotestsum: reading stderr: %w", err)
	}
	return splitLines(string(o)), splitLines(string(e)), nil
}

type block struct {
	kind   string // FAIL or SKIP
	header string
	body   []string
}

// Aggressive keeps the failure summary and the final DONE line.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if declined(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}

	var live []string
	sections := map[string][]string{}
	var done string
	cur := ""
	for _, l := range stdout {
		switch {
		case sectionRE.MatchString(l):
			cur = sectionRE.FindStringSubmatch(l)[1]
			sections[cur] = []string{}
		case doneRE.MatchString(l):
			done = l
			cur = "done"
		case cur == "":
			live = append(live, l)
		case cur != "done":
			sections[cur] = append(sections[cur], l)
		}
	}
	if done == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	_, hasFailed := sections["Failed"]
	_, hasErrors := sections["Errors"]
	if in.ExitCode != 0 && !hasFailed && !hasErrors {
		// Nothing proves the failure output is repeated in a summary.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// The live feed is progress plus a copy of what the summary repeats;
	// it is counted, not kept.
	droppedLive := 0
	for _, l := range live {
		if strings.TrimSpace(l) != "" {
			droppedLive++
		}
	}

	var b strings.Builder
	droppedFrames := 0
	for _, blk := range parseBlocks(sections["Failed"], "FAIL") {
		b.WriteString(blk.header + "\n")
		body, n := compactStack(trimBlank(blk.body))
		droppedFrames += n
		for _, l := range body {
			b.WriteString(l + "\n")
		}
	}
	if errs := trimBlank(sections["Errors"]); len(errs) > 0 {
		b.WriteString("Errors:\n")
		for _, l := range errs {
			b.WriteString(l + "\n")
		}
	}
	skips := parseBlocks(sections["Skipped"], "SKIP")
	for i, blk := range skips {
		if i == maxSkips {
			fmt.Fprintf(&b, "…+%d more skipped\n", len(skips)-maxSkips)
			break
		}
		line := blk.header
		if reason := strings.TrimSpace(strings.Join(trimBlank(blk.body), " ")); reason != "" {
			line += ": " + reason
		}
		b.WriteString(line + "\n")
	}
	for _, l := range stderr {
		b.WriteString(l + "\n")
	}
	b.WriteString(done + "\n")
	var parts []string
	if droppedLive > 0 {
		parts = append(parts, fmt.Sprintf("live output ×%d", droppedLive))
	}
	if droppedFrames > 0 {
		parts = append(parts, fmt.Sprintf("stdlib frames ×%d", droppedFrames))
	}
	if len(skips) > maxSkips {
		parts = append(parts, fmt.Sprintf("skips ×%d", len(skips)-maxSkips))
	}
	elided := len(parts) > 0
	if elided {
		fmt.Fprintf(&b, "…+%d lines (%s)\n", droppedLive+droppedFrames, strings.Join(parts, ", "))
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: true, Elided: elided}, nil
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return lines
}

// parseBlocks splits a summary section into per-test blocks, rewriting each
// "=== FAIL: pkg Test (0.00s)" header to "FAIL pkg Test" (the duration is
// progress noise; a "(re-run N)" marker is kept).
func parseBlocks(lines []string, kind string) []block {
	var blocks []block
	for _, l := range lines {
		if m := blockRE.FindStringSubmatch(l); m != nil && m[1] == kind {
			h := kind + " " + m[2]
			if m[3] != "" {
				h += " " + m[3]
			}
			blocks = append(blocks, block{kind: kind, header: h})
			continue
		}
		if len(blocks) == 0 {
			continue
		}
		blocks[len(blocks)-1].body = append(blocks[len(blocks)-1].body, l)
	}
	return blocks
}

// Relaxed drops passing/skipped progress lines and collapses toolchain stack
// frames; every other line is kept, so it is safe when no summary exists.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if declined(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	var kept []string
	dropped := 0
	for _, l := range stdout {
		if liveNoise.MatchString(l) {
			dropped++
			continue
		}
		kept = append(kept, l)
	}
	kept, frames := compactStack(kept)
	if dropped+frames == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, stderr...)
	var parts []string
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("progress ×%d", dropped))
	}
	if frames > 0 {
		parts = append(parts, fmt.Sprintf("stdlib frames ×%d", frames))
	}
	body := strings.Join(kept, "\n") + fmt.Sprintf("\n…+%d lines (%s)\n", dropped+frames, strings.Join(parts, ", "))
	return format.Rendered{Body: []byte(body), FoldStderr: true, Elided: true}, nil
}
