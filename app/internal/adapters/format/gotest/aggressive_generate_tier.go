package gotest

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveGenerate implements the aggressive tier for `go generate`. It
// dedupes repeated lines (generators often re-announce identical directives
// across files) and strips progress noise, keeping every error line
// verbatim. Quiet, successful runs have nothing worth compressing and
// degrade to the next tier.
func aggressiveGenerate(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := dedupeLines(append(splitLines(string(stdout)), splitLines(string(stderr))...))
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}

	body := strings.Join(kept, "\n")
	if body != "" {
		body += "\n"
	}
	if in.ExitCode == 0 && (body == "" || len(body) >= len(stdout)+len(stderr)) {
		// Little or nothing to say and it succeeded: not worth rendering.
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if body == "" {
		body = fmt.Sprintf("generate: failed (exit %d), no diagnostic captured\n", in.ExitCode)
	}

	return format.Rendered{Body: []byte(body), FoldStderr: len(stderr) > 0}, nil
}
