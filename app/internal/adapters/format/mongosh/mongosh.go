// Package mongosh implements a format.Formatter for `mongosh` (the MongoDB
// Shell). sctx is the first output wrapper to
// compress it. Agents typically run `mongosh --quiet --json=relaxed --eval
// '...'` (structured JSON documents) or the plain `mongosh --eval '...'`
// (shell-object output with ObjectId()/ISODate() wrappers); both can print a
// large connection banner and large document arrays.
package mongosh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `mongosh` output.
type Formatter struct{}

// New constructs a mongosh Formatter.
func New() format.Formatter { return &Formatter{} }

// Descriptor claims the bare "mongosh" program; the interesting behavior
// lives in --eval, not a subcommand, so no Subcommands are declared.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "mongosh"}
}

// Aggressive strips the connection banner, then either delegates to
// jsoncompact (when stdout is valid JSON, i.e. --json=relaxed output) or
// applies a heuristic top-level-document-array compaction for the default
// shell-object output. A non-zero exit degrades to the relaxed tier so error
// text is never lost to a failed structured parse.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("mongosh: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("mongosh: reading stderr: %w", err)
	}

	cleanedOut := stripBanner(stdout)
	cleanedErr := stripBanner(stderr)
	// If stderr carried only banner noise (some mongosh versions print the
	// preamble there), it's safe to fold away entirely.
	foldStderr := len(stderr) > 0 && len(cleanedErr) == 0

	trimmed := bytes.TrimSpace(cleanedOut)
	if len(trimmed) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if json.Valid(trimmed) {
		jsonIn := format.Input{
			Argv:     in.Argv,
			Command:  in.Command,
			Stdout:   bytes.NewReader(trimmed),
			ExitCode: in.ExitCode,
			Duration: in.Duration,
		}
		rendered, err := jsoncompact.New().Aggressive(ctx, jsonIn)
		if err != nil {
			// Propagate ErrTierInapplicable (tiny/incompressible payload)
			// or a genuine anomaly unchanged.
			return format.Rendered{}, err
		}
		rendered.Note = "mongosh " + rendered.Note
		rendered.FoldStderr = foldStderr
		return rendered, nil
	}

	rendered, err := compactDocumentArray(trimmed)
	if err != nil {
		return format.Rendered{}, err
	}
	rendered.FoldStderr = foldStderr
	return rendered, nil
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
