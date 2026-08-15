package golangcilint

import (
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// relaxedFilter only collapses provably redundant runs and marks every
// elision. In particular, verbose level=info output remains visible because it
// was explicitly requested and can contain timing and configuration evidence.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)
	if looksMachineReadable(rawStdout) || looksMachineReadable(rawStderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdoutLines, stdoutChanged := collapse.Runs(collapse.SplitLines(rawStdout))
	stderrLines, stderrChanged := collapse.Runs(collapse.SplitLines(rawStderr))
	if !stdoutChanged && !stderrChanged {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	body := append([]string(nil), stdoutLines...)
	body = append(body, stderrLines...)
	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(rawStderr) > 0,
	}, nil
}

func looksMachineReadable(raw []byte) bool {
	for _, line := range collapse.SplitLines(raw) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "{") ||
			strings.HasPrefix(trimmed, "[") ||
			strings.HasPrefix(trimmed, "<") ||
			strings.HasPrefix(trimmed, "##teamcity[") ||
			strings.HasPrefix(trimmed, "::error") ||
			strings.Contains(line, "\t")
	}
	return false
}
