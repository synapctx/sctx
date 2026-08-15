package docker

import (
	"bytes"
	"context"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/dockerargv"
	"github.com/synapctx/sctx/internal/platform/progkey"
)

func dockerTransportFailure(stderr []byte) bool {
	lower := strings.ToLower(string(stderr))
	for _, marker := range []string{
		"cannot connect to the docker daemon",
		"error response from daemon",
		"permission denied while trying to connect to the docker api",
		"is the docker daemon running",
		"oci runtime exec failed",
		"no such container",
		"container is not running",
		"no configuration file provided",
		"no such service",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (f *Formatter) execInner(in format.Input, sub string, rest []string) (format.Formatter, format.Input, error) {
	if f.resolve == nil {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	argv, ok := dockerargv.ExecCommand(sub, rest)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	fm, ok := f.resolve(argv)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	if dockerTransportFailure(rawErr) {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	next := ""
	if len(argv) > 1 {
		next = argv[1]
	}
	return fm, format.Input{
		Argv: argv, Command: progkey.Key(argv[0], next),
		Stdout: bytes.NewReader(rawOut), Stderr: bytes.NewReader(rawErr),
		ExitCode: in.ExitCode, Duration: in.Duration,
	}, nil
}

func (f *Formatter) aggressiveExec(ctx context.Context, in format.Input, sub string, rest []string) (format.Rendered, error) {
	fm, inner, err := f.execInner(in, sub, rest)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Aggressive(ctx, inner)
}

func (f *Formatter) relaxedExec(ctx context.Context, in format.Input, sub string, rest []string) (format.Rendered, error) {
	fm, inner, err := f.execInner(in, sub, rest)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Relaxed(ctx, inner)
}
