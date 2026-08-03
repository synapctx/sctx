// Package read implements a shared format.Formatter for cat, head, and tail:
// three thin instances over one set of internals, since these commands
// render identically (JSON/JSONL-sniff delegation on the aggressive tier,
// generic blank/dupe/timestamped-repeat collapsing on the relaxed tier).
//
// Conservative-by-design: unlike other formatters, cat/head/tail output is
// file content the agent explicitly asked to see. This package only ever
// compresses content it can prove is redundant or self-describing structured
// data — a single JSON document, newline-delimited JSON, or exact/near-exact
// duplicate log lines — and every elision carries an explicit marker
// ("…+N", "×N"). It must NEVER truncate or hide arbitrary prose or source
// code; when a render can't be proven safe it returns
// format.ErrTierInapplicable so the raw content passes through unchanged.
// Do not "improve" this into capping plain text/code files — that is the
// one thing this package must never do.
package read

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/domain/format"
)

// formatter implements format.Formatter for cat/head/tail.
type formatter struct {
	command string
}

// New returns the cat formatter.
func New() format.Formatter { return &formatter{command: "cat"} }

// All returns the cat, head, and tail formatters, sharing internals.
func All() []format.Formatter {
	return []format.Formatter{
		&formatter{command: "cat"},
		&formatter{command: "head"},
		&formatter{command: "tail"},
	}
}

func (f *formatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

// Aggressive only ever compacts content that is provably structured data,
// never prose or source: it (1) delegates to the JSON compactor when stdout
// sniffs as a single JSON document (first non-whitespace byte '{' or '['),
// and (2), if that's inapplicable, checks whether stdout is newline-
// delimited JSON (JSONL/NDJSON, e.g. a log file) and if so keeps the first
// few lines and elides the rest with an explicit marker (see jsonl.go).
// Anything else — including a file that merely contains a `{`/`}` here and
// there, like source code — is left untouched and falls through to the
// relaxed tier. This is deliberate: cat/head/tail output is content the
// agent explicitly asked to read, so this formatter must never hide or
// truncate arbitrary text behind an elision marker, only genuinely
// redundant/structured data. When in doubt, degrade.
//
// The content sniffer in the run pipeline only fires for commands no
// formatter claims, so cat/head/tail claiming the command would otherwise
// shadow JSON compaction for e.g. `cat file.json`; the delegation below
// restores it.
func (f *formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		// A non-zero exit almost always means an error message (e.g. "No
		// such file or directory") that neither this tier nor the JSON
		// compactor understands; degrade to relaxed line filtering, which
		// preserves it verbatim.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("read: reading stdout: %w", err)
	}

	if sniffsJSON(raw) {
		sub := format.Input{
			Argv:     in.Argv,
			Command:  in.Command,
			Stdout:   bytes.NewReader(raw),
			Stderr:   in.Stderr,
			ExitCode: in.ExitCode,
			Duration: in.Duration,
		}
		out, jerr := jsoncompact.New().Aggressive(ctx, sub)
		switch {
		case jerr == nil:
			return out, nil
		case !errors.Is(jerr, format.ErrTierInapplicable):
			// A real error (not "try the next tier"); propagate as-is.
			return format.Rendered{}, jerr
		}
		// Starts with '{'/'[' but isn't one valid JSON document — most
		// likely JSONL (one object per line). Fall through and check.
	}

	if out, ok := renderJSONL(raw); ok {
		return out, nil
	}

	return format.Rendered{}, format.ErrTierInapplicable
}

// sniffsJSON reports whether raw's first non-whitespace byte opens a JSON
// object or array.
func sniffsJSON(raw []byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
