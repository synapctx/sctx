package gotest

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/domain/format"
)

// maxListEntries caps how many plain module/package names `go list` keeps
// before collapsing the remainder to a "…+N more" marker.
const maxListEntries = 20

// aggressiveList implements the aggressive tier for `go list` (-m all,
// ./..., -json, ...). JSON output (from -json / -json=...) is delegated to
// jsoncompact for structured elision; plain text output is a flat list of
// module or package paths, capped with an explicit marker.
func aggressiveList(ctx context.Context, in format.Input) (format.Rendered, error) {
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

	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) > 0 && jsontext.Value(trimmed).IsValid() {
		jc := jsoncompact.New()
		rendered, err := jc.Aggressive(ctx, format.Input{Stdout: bytes.NewReader(stdout)})
		if err == nil {
			rendered.FoldStderr = rendered.FoldStderr || len(stderr) > 0
			if len(stderr) > 0 {
				rendered.Body = append(rendered.Body, '\n')
				rendered.Body = append(rendered.Body, stderr...)
			}
			return rendered, nil
		}
		if err != format.ErrTierInapplicable {
			return format.Rendered{}, err
		}
		// Fall through to plain-text handling below.
	}

	lines := splitLines(string(stdout))
	capped := lines
	if len(lines) > maxListEntries {
		more := len(lines) - maxListEntries
		capped = make([]string, 0, maxListEntries+1)
		capped = append(capped, lines[:maxListEntries]...)
		capped = append(capped, fmt.Sprintf("…+%d more", more))
	}

	body := strings.Join(capped, "\n")
	if body != "" {
		body += "\n"
	}
	foldStderr := false
	if len(stderr) > 0 {
		body += string(stderr)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		foldStderr = true
	}
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if len(stderr) == 0 && len(body) >= len(stdout) {
		// Nothing was actually saved (short list under the cap) and there
		// is no error signal to surface; let a later tier decide.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}, nil
}
