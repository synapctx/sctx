// Package nested contains the shared mechanics for delegating a transport's
// captured streams to the formatter for its finite inner command.
package nested

import (
	"bytes"
	"context"
	"io"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/nestedcmd"
	"github.com/synapctx/sctx/internal/platform/progkey"
)

type Resolver func(argv []string) (format.Formatter, bool)
type TransportFailure func(stderr []byte, exitCode int) bool

// Prepare validates and resolves argv, snapshots both streams, rejects output
// belonging to the transport itself, and constructs the inner formatter input.
func Prepare(resolve Resolver, in format.Input, argv []string, failed TransportFailure) (format.Formatter, format.Input, error) {
	if resolve == nil {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	argv, ok := nestedcmd.Direct(argv)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return nil, format.Input{}, err
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return nil, format.Input{}, err
	}
	if failed != nil && failed(rawErr, in.ExitCode) {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	fm, ok := resolve(argv)
	if !ok || fm == nil {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	return fm, format.Input{
		Argv: argv, Command: progkey.FromArgv(argv),
		Stdout: bytes.NewReader(rawOut), Stderr: bytes.NewReader(rawErr),
		ExitCode: in.ExitCode, Duration: in.Duration,
	}, nil
}

func Aggressive(ctx context.Context, resolve Resolver, in format.Input, argv []string, failed TransportFailure) (format.Rendered, error) {
	fm, inner, err := Prepare(resolve, in, argv, failed)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Aggressive(ctx, inner)
}

func Relaxed(ctx context.Context, resolve Resolver, in format.Input, argv []string, failed TransportFailure) (format.Rendered, error) {
	fm, inner, err := Prepare(resolve, in, argv, failed)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Relaxed(ctx, inner)
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
