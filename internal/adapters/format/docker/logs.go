package docker

import (
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveLogs treats `docker logs` output as log noise rather than a
// table. It collapses only exact consecutive repetitions, independently on
// stdout and stderr, so a unique middle diagnostic can never disappear.
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
		parts = append(parts, filterRelaxedLines(outLines)...)
	}
	foldStderr := false
	if len(errLines) > 0 {
		parts = append(parts, filterRelaxedLines(errLines)...)
		foldStderr = true
	}
	return format.Rendered{Body: []byte(strings.Join(parts, "\n")), FoldStderr: foldStderr}, nil
}
