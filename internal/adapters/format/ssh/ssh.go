// Package ssh implements a format.Formatter for `ssh` by delegating to the formatter for
// the REMOTE command.
//
// ssh was the largest real entry on the coverage-gap meter after decontamination (~37
// delegations in 7 days), and it looks unformattable: its output is whatever ran on the
// far end. But that is only true if you stop at the program name. `ssh host 'docker ps'`
// produces docker's output, and sctx already knows how to render that — the remote command
// is right there in argv. So instead of one more formatter, this makes every existing
// formatter work over ssh.
//
// The delegation is deliberately narrow, because the failure mode is handing arbitrary
// remote output to a parser that will confidently misread it. It applies only when the
// remote command is a single simple command whose program sctx has a formatter for; every
// other shape declines and the tier chain degrades to verbatim. The inner formatter's own
// recognition guard is the second line of defence: ssh may interleave a banner, a MOTD or a
// host-key warning, and a formatter that cannot recognise its own input must decline rather
// than parse it.
package ssh

import (
	"context"

	"github.com/synapctx/sctx/internal/adapters/format/nested"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/sshargv"
)

// Resolver reports which formatter handles a command, given its argv.
//
// Declared here rather than imported so this adapter does not depend on the application
// layer that owns the registry; main.go supplies registry.ResolveByArgv.
type Resolver = nested.Resolver

// Formatter renders `ssh` output by deferring to the remote command's formatter.
type Formatter struct {
	resolve Resolver
}

// New constructs an ssh Formatter. A nil resolver makes it inert: every tier declines,
// which is the correct behaviour for a delegating formatter with nothing to delegate to.
func New(resolve Resolver) format.Formatter { return &Formatter{resolve: resolve} }

func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "ssh"}
}

// sshExitFailure is the exit code ssh reserves for its OWN failures — connection refused,
// authentication failed, host key mismatch. The output then belongs to ssh, not to the
// remote program, and must not be handed to the remote program's parser.
const sshExitFailure = 255

// remoteArgv extracts the remote command from an ssh invocation, or reports false when
// there is nothing safely delegatable.
func remoteArgv(argv []string) ([]string, bool) {
	return sshargv.RemoteCommand(argv)
}

func sshTransportFailure(_ []byte, exitCode int) bool {
	return exitCode == sshExitFailure
}

// Aggressive delegates to the remote command's aggressive tier.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	rargv, ok := remoteArgv(in.Argv)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return nested.Aggressive(ctx, nested.Resolver(f.resolve), in, rargv, sshTransportFailure)
}

// Relaxed delegates to the remote command's relaxed tier.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	rargv, ok := remoteArgv(in.Argv)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return nested.Relaxed(ctx, nested.Resolver(f.resolve), in, rargv, sshTransportFailure)
}
