// Package golangcilint implements a format.Formatter for golangci-lint. It
// claims every golangci-lint invocation and only applies structured
// formatting to the "run" subcommand, since that is the only one whose
// output has a stable, parseable shape.
package golangcilint

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var globalFlagsWithValue = map[string]bool{
	"--color": true,
}

// Formatter renders golangci-lint command output.
type Formatter struct{}

// New constructs a golangci-lint Formatter.
func New() *Formatter {
	return &Formatter{}
}

// Descriptor claims all golangci-lint invocations; subcommand dispatch
// happens inside Aggressive.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "golangci-lint"}
}

// subcommand returns the golangci-lint subcommand (e.g. "run"), skipping
// argv[0] and any leading flags.
func subcommand(argv []string) string {
	if len(argv) <= 1 {
		return ""
	}
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			name := a
			if before, _, ok := strings.Cut(a, "="); ok {
				name = before
			}
			if globalFlagsWithValue[name] && !strings.Contains(a, "=") && i+1 < len(argv) {
				i++
			}
			continue
		}
		return a
	}
	return ""
}

// Aggressive parses `golangci-lint run` output into a grouped issue listing.
//
// golangci-lint normally exits 1 when issues are found, but users can change
// that value with --issues-exit-code. Output recognition, rather than the exit
// code, therefore decides whether this tier applies. Configuration and runtime
// failures do not contain a valid issue listing and degrade naturally.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if subcommand(in.Argv) != "run" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if requestsMachineOutputOnCapturedStream(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return aggressiveRun(in)
}

// Relaxed applies heuristic line-level filtering to any golangci-lint
// invocation.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if requestsMachineOutputOnCapturedStream(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return relaxedFilter(in)
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

// splitLines splits raw bytes into lines without trailing newlines, dropping
// a single final empty element caused by a trailing newline.
func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
