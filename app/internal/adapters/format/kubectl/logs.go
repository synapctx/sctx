package kubectl

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// logsHeadLines and logsTailLines bound the amount of log noise kept when a
// log stream exceeds logsThreshold lines.
const (
	logsHeadLines = 5
	logsTailLines = 20
	logsThreshold = logsHeadLines + logsTailLines
)

// headTailLines keeps the first logsHeadLines and last logsTailLines lines
// of a log stream once it exceeds logsThreshold lines, with an explicit
// elision marker in between. Timestamps and other content are left intact.
func headTailLines(lines []string) []string {
	if len(lines) <= logsThreshold {
		return lines
	}
	head := lines[:logsHeadLines]
	tail := lines[len(lines)-logsTailLines:]
	elided := len(lines) - logsHeadLines - logsTailLines
	out := make([]string, 0, logsHeadLines+1+logsTailLines)
	out = append(out, head...)
	out = append(out, fmt.Sprintf("…+%d lines", elided))
	out = append(out, tail...)
	return out
}

// aggressiveLogs treats `kubectl logs` output as log noise rather than a
// table, applying the head/tail elision policy independently to stdout and
// stderr.
func aggressiveLogs(in format.Input) (format.Rendered, error) {
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	outLines := splitLines(rawOut)
	errLines := splitLines(rawErr)
	if len(outLines) == 0 && len(errLines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var parts []string
	if len(outLines) > 0 {
		parts = append(parts, headTailLines(outLines)...)
	}
	foldStderr := false
	if len(errLines) > 0 {
		parts = append(parts, headTailLines(errLines)...)
		foldStderr = true
	}
	return format.Rendered{Body: []byte(strings.Join(parts, "\n")), FoldStderr: foldStderr}, nil
}
