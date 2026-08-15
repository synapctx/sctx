package kubectl

import (
	"context"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/nested"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/kubectlargv"
)

// execArgv extracts the command after kubectl exec's mandatory `--`
// separator. Interactive sessions are deliberately excluded because sctx
// buffers until EOF and would break the terminal interaction.
func execArgv(rest []string) ([]string, bool) {
	return kubectlargv.ExecCommand(rest)
}

func kubectlTransportFailure(stderr []byte) bool {
	for _, line := range splitLines(stderr) {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "command terminated with exit code "):
			continue
		case strings.HasPrefix(trimmed, "Defaulted container "), strings.HasPrefix(trimmed, "Warning:"):
			continue
		case strings.HasPrefix(trimmed, "error:"),
			strings.HasPrefix(trimmed, "Error from server"),
			strings.HasPrefix(trimmed, "Unable to connect to the server"),
			strings.HasPrefix(trimmed, "The connection to the server"):
			return true
		}
	}
	return false
}

func kubectlExecTransportFailure(stderr []byte, _ int) bool {
	return kubectlTransportFailure(stderr)
}

func (f *Formatter) aggressiveExec(ctx context.Context, in format.Input, rest []string) (format.Rendered, error) {
	argv, ok := execArgv(rest)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return nested.Aggressive(ctx, nested.Resolver(f.resolve), in, argv, kubectlExecTransportFailure)
}

func (f *Formatter) relaxedExec(ctx context.Context, in format.Input, rest []string) (format.Rendered, error) {
	argv, ok := execArgv(rest)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return nested.Relaxed(ctx, nested.Resolver(f.resolve), in, argv, kubectlExecTransportFailure)
}
