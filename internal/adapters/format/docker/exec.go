package docker

import (
	"context"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/nested"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/dockerargv"
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

func dockerExecTransportFailure(stderr []byte, _ int) bool {
	return dockerTransportFailure(stderr)
}

func (f *Formatter) aggressiveExec(ctx context.Context, in format.Input, sub string, rest []string) (format.Rendered, error) {
	argv, ok := dockerargv.ExecCommand(sub, rest)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return nested.Aggressive(ctx, nested.Resolver(f.resolve), in, argv, dockerExecTransportFailure)
}

func (f *Formatter) relaxedExec(ctx context.Context, in format.Input, sub string, rest []string) (format.Rendered, error) {
	argv, ok := dockerargv.ExecCommand(sub, rest)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return nested.Relaxed(ctx, nested.Resolver(f.resolve), in, argv, dockerExecTransportFailure)
}
