package gotest

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// aggressiveBuildVet implements the aggressive tier for `go build` and
// `go vet`. Success is usually silent, so empty output degrades to the
// next tier; failures fold stderr into Body with diagnostics deduped.
func aggressiveBuildVet(in format.Input, subcmd string) (format.Rendered, error) {
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

	allLines := dedupeLines(append(splitLines(string(stdout)), splitLines(string(stderr))...))
	kept := make([]string, 0, len(allLines))
	for _, l := range allLines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}

	body := strings.Join(kept, "\n")
	switch {
	case body != "":
		body += "\n"
	case in.ExitCode != 0:
		body = fmt.Sprintf("%s: failed (exit %d), no diagnostic captured\n", subcmd, in.ExitCode)
	default:
		body = fmt.Sprintf("%s: ok\n", subcmd)
	}

	return format.Rendered{Body: []byte(body), FoldStderr: len(stderr) > 0}, nil
}
