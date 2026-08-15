package kubectl

import (
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// relaxedCommands are kubectl commands whose default human output can safely
// benefit from exact run collapse. Unknown commands remain byte-for-byte
// verbatim: completion scripts, diffs, generated YAML, JSONPath, and plugin
// output are data contracts rather than prose.
var relaxedCommands = map[string]bool{
	"get": true, "describe": true, "logs": true, "top": true,
	"events": true, "rollout": true, "api-resources": true,
	"apply": true, "create": true, "delete": true, "patch": true,
	"scale": true, "label": true, "annotate": true,
}

// relaxedFilter only collapses consecutive identical lines and marks every
// elision. Blank lines and separators are retained because they can carry
// structure in kubectl's YAML, templates, diffs, and human descriptions.
func relaxedFilter(in format.Input) (format.Rendered, error) {
	sub, _ := subcommand(in.Argv)
	if !relaxedCommands[sub] {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	if looksStructuredData(rawOut) || looksStructuredData(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	outLines, outChanged := collapse.Runs(collapse.SplitLines(rawOut))
	errLines, errChanged := collapse.Runs(collapse.SplitLines(rawErr))
	if !outChanged && !errChanged {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := append([]string(nil), outLines...)
	body = append(body, errLines...)
	return format.Rendered{
		Body:       []byte(strings.Join(body, "\n")),
		FoldStderr: len(rawErr) > 0,
	}, nil
}

func looksStructuredData(raw []byte) bool {
	for _, line := range collapse.SplitLines(raw) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "---") {
			continue
		}
		return strings.HasPrefix(trimmed, "{") ||
			strings.HasPrefix(trimmed, "[") ||
			strings.HasPrefix(trimmed, "<") ||
			strings.HasPrefix(trimmed, "apiVersion:") ||
			strings.HasPrefix(trimmed, "#compdef") ||
			strings.Contains(line, "\t")
	}
	return false
}
