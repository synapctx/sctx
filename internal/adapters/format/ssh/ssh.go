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
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/progkey"
)

// Resolver reports which formatter handles a command, given its argv.
//
// Declared here rather than imported so this adapter does not depend on the application
// layer that owns the registry; main.go supplies registry.ResolveByArgv.
type Resolver func(argv []string) (format.Formatter, bool)

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

// optsWithValue are the ssh options that consume the following argument. Getting this list
// wrong shifts which argv element is read as the host, and the remote command with it: in
// `ssh -p 2222 host uptime`, treating -p as valueless makes "2222" the host and
// "host uptime" the command.
var optsWithValue = map[byte]bool{
	'B': true, 'b': true, 'c': true, 'D': true, 'E': true, 'e': true, 'F': true,
	'I': true, 'i': true, 'J': true, 'L': true, 'l': true, 'm': true, 'O': true,
	'o': true, 'p': true, 'Q': true, 'R': true, 'S': true, 'W': true, 'w': true,
}

// noRemoteCommand are the flags that mean "do not run a command", so anything that follows
// is not one.
var noRemoteCommand = map[byte]bool{'N': true, 'T': true}

// remoteArgv extracts the remote command from an ssh invocation, or reports false when
// there is nothing safely delegatable.
func remoteArgv(argv []string) ([]string, bool) {
	if len(argv) < 2 {
		return nil, false
	}
	i := 1
	for i < len(argv) {
		a := argv[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		if a == "--" {
			i++
			break
		}
		if strings.HasPrefix(a, "--") {
			// ssh has no long options; something unusual is going on, so stop guessing.
			return nil, false
		}
		// A cluster such as -tq, or -p2222 with the value attached.
		consumed := false
		for k := 1; k < len(a); k++ {
			c := a[k]
			if noRemoteCommand[c] {
				return nil, false
			}
			if optsWithValue[c] {
				// The value is either the rest of this token or the next one.
				if k == len(a)-1 {
					i++ // value is the next argv element
				}
				consumed = true
				break
			}
		}
		_ = consumed
		i++
	}
	if i >= len(argv) {
		return nil, false // options only: no host
	}
	// argv[i] is [user@]host. Anything after it is the remote command.
	rest := argv[i+1:]
	if len(rest) == 0 {
		return nil, false // interactive session: nothing to format
	}
	// `ssh host 'docker ps'` arrives as ONE argv element because the local shell removed
	// the quotes; `ssh host docker ps` arrives as two. Both are valid, so normalise by
	// joining and re-splitting on whitespace.
	joined := strings.TrimSpace(strings.Join(rest, " "))
	if joined == "" {
		return nil, false
	}
	if isCompound(joined) {
		// Two or more programs produced this output; one formatter cannot render it, and
		// guessing which half it belongs to would misreport both.
		return nil, false
	}
	fields := strings.Fields(joined)
	if len(fields) == 0 {
		return nil, false
	}
	switch fields[0] {
	case "ssh", "sctx":
		// `ssh a 'ssh b ...'` would recurse through this same formatter, and wrapping an
		// already-wrapped command is meaningless.
		return nil, false
	}
	return fields, true
}

// isCompound reports whether s contains shell syntax that makes it more than one simple
// command. Quoted occurrences are not distinguished: this is a conservative gate, and
// treating a quoted `;` as compound costs a missed optimisation while the reverse costs a
// misparsed render.
func isCompound(s string) bool {
	return strings.ContainsAny(s, ";|&\n`") ||
		strings.Contains(s, "$(") ||
		strings.Contains(s, "&&") ||
		strings.Contains(s, "||") ||
		strings.Contains(s, ">") ||
		strings.Contains(s, "<")
}

// inner resolves the formatter for the remote command and builds the Input to hand it.
func (f *Formatter) inner(in format.Input) (format.Formatter, format.Input, error) {
	if f.resolve == nil {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	if in.ExitCode == sshExitFailure {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	rargv, ok := remoteArgv(in.Argv)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	fm, ok := f.resolve(rargv)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	var next string
	if len(rargv) > 1 {
		next = rargv[1]
	}
	// The remote command's exit code IS ssh's exit code (except 255, excluded above), so
	// passing it through keeps the inner formatter's failure handling correct.
	return fm, format.Input{
		Argv:     rargv,
		Command:  progkey.Key(rargv[0], next),
		Stdout:   in.Stdout,
		Stderr:   in.Stderr,
		ExitCode: in.ExitCode,
		Duration: in.Duration,
	}, nil
}

// Aggressive delegates to the remote command's aggressive tier.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	fm, innerIn, err := f.inner(in)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Aggressive(ctx, innerIn)
}

// Relaxed delegates to the remote command's relaxed tier.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	fm, innerIn, err := f.inner(in)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Relaxed(ctx, innerIn)
}
