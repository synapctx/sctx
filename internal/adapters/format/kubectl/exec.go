package kubectl

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/kubectlargv"
	"github.com/synapctx/sctx/internal/platform/progkey"
)

// execArgv extracts the command after kubectl exec's mandatory `--`
// separator. Interactive sessions are deliberately excluded because sctx
// buffers until EOF and would break the terminal interaction.
func execArgv(rest []string) ([]string, bool) {
	separator := -1
	for i, arg := range rest {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 1 || separator+1 >= len(rest) {
		return nil, false
	}
	before := rest[:separator]
	if kubectlargv.HasFlag(before, "-i", "--stdin", "-t", "--tty") {
		return nil, false
	}
	inner := rest[separator+1:]
	if len(inner) == 0 || isShell(inner[0]) || filepath.Base(inner[0]) == "sctx" {
		return nil, false
	}
	return inner, true
}

func isShell(program string) bool {
	switch filepath.Base(program) {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh":
		return true
	default:
		return false
	}
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

func (f *Formatter) execInner(in format.Input, rest []string) (format.Formatter, format.Input, error) {
	if f.resolve == nil {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	argv, ok := execArgv(rest)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	fm, ok := f.resolve(argv)
	if !ok {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	rawOut := readAll(in.Stdout)
	rawErr := readAll(in.Stderr)
	if kubectlTransportFailure(rawErr) {
		return nil, format.Input{}, format.ErrTierInapplicable
	}
	next := ""
	if len(argv) > 1 {
		next = argv[1]
	}
	return fm, format.Input{
		Argv:     argv,
		Command:  progkey.Key(argv[0], next),
		Stdout:   bytes.NewReader(rawOut),
		Stderr:   bytes.NewReader(rawErr),
		ExitCode: in.ExitCode,
		Duration: in.Duration,
	}, nil
}

func (f *Formatter) aggressiveExec(ctx context.Context, in format.Input, rest []string) (format.Rendered, error) {
	fm, inner, err := f.execInner(in, rest)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Aggressive(ctx, inner)
}

func (f *Formatter) relaxedExec(ctx context.Context, in format.Input, rest []string) (format.Rendered, error) {
	fm, inner, err := f.execInner(in, rest)
	if err != nil {
		return format.Rendered{}, err
	}
	return fm.Relaxed(ctx, inner)
}
