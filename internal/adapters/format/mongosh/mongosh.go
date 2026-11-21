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
	"encoding/json/jsontext"
	"errors"
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

	if jsontext.Value(trimmed).IsValid() {
		rendered, err := compactJSON(ctx, in, trimmed)
		if err != nil {
			return format.Rendered{}, err
		}
		rendered.FoldStderr = foldStderr
		return rendered, nil
	}

	rendered, err := compactDocumentArray(trimmed)
	if errors.Is(err, format.ErrTierInapplicable) {
		rendered, err = compactDocument(trimmed)
	}
	if err != nil {
		return format.Rendered{}, err
	}
	rendered.FoldStderr = foldStderr
	return rendered, nil
}

// compactJSON renders --json output. Query results are the answer, so the
// whole value is kept (whitespace and key repetition only); a top-level array
// above documentCap keeps docHead + docTail documents with an explicit marker
// element in between.
func compactJSON(ctx context.Context, in format.Input, trimmed []byte) (format.Rendered, error) {
	body, omitted := capJSONArray(trimmed)
	jsonIn := format.Input{
		Argv:     in.Argv,
		Command:  in.Command,
		Stdout:   bytes.NewReader(body),
		ExitCode: in.ExitCode,
		Duration: in.Duration,
	}
	rendered, err := jsoncompact.New().Relaxed(ctx, jsonIn)
	if errors.Is(err, format.ErrTierInapplicable) && omitted > 0 {
		c := jsontext.Value(body).Clone()
		if cerr := c.Compact(); cerr != nil {
			return format.Rendered{}, fmt.Errorf("mongosh: compacting json: %w", cerr)
		}
		rendered, err = format.Rendered{Body: c}, nil
	}
	if err != nil {
		return format.Rendered{}, err
	}
	rendered.Note = fmt.Sprintf("mongosh json (%d documents omitted)", omitted)
	rendered.Elided = omitted > 0
	return rendered, nil
}

// capJSONArray returns raw unchanged unless it is a top-level array with more
// than documentCap elements, in which case the middle is replaced by one
// "…+N more documents" string element. It reports how many were omitted.
func capJSONArray(raw []byte) ([]byte, int) {
	var elems []jsontext.Value
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if k := dec.PeekKind(); k != '[' {
		return raw, 0
	}
	if _, err := dec.ReadToken(); err != nil {
		return raw, 0
	}
	for dec.PeekKind() != ']' {
		v, err := dec.ReadValue()
		if err != nil {
			return raw, 0
		}
		elems = append(elems, v.Clone())
	}
	if len(elems) <= documentCap {
		return raw, 0
	}
	omitted := len(elems) - docHead - docTail
	var b bytes.Buffer
	b.WriteByte('[')
	for i, e := range elems[:docHead] {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(e)
	}
	fmt.Fprintf(&b, `,"…+%d more documents"`, omitted)
	for _, e := range elems[len(elems)-docTail:] {
		b.WriteByte(',')
		b.Write(e)
	}
	b.WriteByte(']')
	return b.Bytes(), omitted
}

// readAll drains a possibly-nil io.Reader.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
