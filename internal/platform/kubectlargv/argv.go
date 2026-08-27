// Package kubectlargv parses the stable, global portion of kubectl's argv.
//
// Keeping this in platform prevents the hook, formatter, and telemetry key
// from maintaining subtly different ideas of where the kubectl command is.
package kubectlargv

import (
	"strings"

	"github.com/synapctx/sctx/internal/platform/nestedcmd"
)

// Invocation is the command selected by kubectl and the arguments after it.
type Invocation struct {
	Command      string
	CommandIndex int
	Args         []string
}

// globalValueFlags is derived from `kubectl options` (v1.36.1). Boolean
// persistent flags are deliberately absent because they do not consume the
// following token.
var globalValueFlags = map[string]bool{
	"--as":                    true,
	"--as-group":              true,
	"--as-uid":                true,
	"--as-user-extra":         true,
	"--cache-dir":             true,
	"--certificate-authority": true,
	"--client-certificate":    true,
	"--client-key":            true,
	"--cluster":               true,
	"--context":               true,
	"--kubeconfig":            true,
	"--kuberc":                true,
	"--log-flush-frequency":   true,
	"--namespace":             true,
	"--password":              true,
	"--profile":               true,
	"--profile-output":        true,
	"--request-timeout":       true,
	"--server":                true,
	"--tls-server-name":       true,
	"--token":                 true,
	"--user":                  true,
	"--username":              true,
	"--v":                     true,
	"--vmodule":               true,
	"-n":                      true,
	"-s":                      true,
	"-v":                      true,
}

// Parse locates kubectl's command after any persistent flags. argv includes
// argv[0]. It declines malformed or unknown global flags rather than guessing
// whether the next token is a flag value or the command.
func Parse(argv []string) (Invocation, bool) {
	if len(argv) < 2 {
		return Invocation{}, false
	}
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return Invocation{}, false
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return Invocation{Command: a, CommandIndex: i, Args: argv[i+1:]}, true
		}

		name, attached := flagName(a)
		if globalValueFlags[name] {
			if attached {
				continue
			}
			if i+1 >= len(argv) {
				return Invocation{}, false
			}
			i++
			continue
		}
		if isKnownBooleanGlobal(name) {
			continue
		}
		return Invocation{}, false
	}
	return Invocation{}, false
}

func flagName(arg string) (name string, attached bool) {
	if before, _, ok := strings.Cut(arg, "="); ok {
		return before, true
	}
	// kubectl/pflag accepts attached values for its persistent shorthand flags.
	if len(arg) > 2 {
		switch arg[:2] {
		case "-n", "-s", "-v":
			return arg[:2], true
		}
	}
	return arg, false
}

func isKnownBooleanGlobal(name string) bool {
	switch name {
	case "--disable-compression", "--insecure-skip-tls-verify", "--match-server-version", "--warnings-as-errors":
		return true
	default:
		return false
	}
}

// OptionValue finds a command-local option before a `--` command separator.
// Both --long=value and separate-value forms are supported.
func OptionValue(args []string, names ...string) (string, bool) {
	for i := range args {
		if args[i] == "--" {
			break
		}
		for _, name := range names {
			if args[i] == name {
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", false
			}
			if after, ok := strings.CutPrefix(args[i], name+"="); ok {
				return after, true
			}
			if len(name) == 2 && strings.HasPrefix(name, "-") && !strings.HasPrefix(args[i], "--") && strings.HasPrefix(args[i], name) && len(args[i]) > 2 {
				return strings.TrimPrefix(args[i], name), true
			}
		}
	}
	return "", false
}

// HasFlag reports whether a command-local boolean flag is enabled before
// `--`. An explicit =false disables it. Short flag clusters such as -it are
// recognized when the requested name is a one-letter shorthand.
func HasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		for _, name := range names {
			if arg == name {
				return true
			}
			if after, ok := strings.CutPrefix(arg, name+"="); ok {
				return !strings.EqualFold(after, "false")
			}
			if len(name) == 2 && strings.HasPrefix(name, "-") && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 2 && strings.ContainsRune(arg[1:], rune(name[1])) {
				return true
			}
		}
	}
	return false
}

// ExecCommand extracts the finite direct command following kubectl exec's
// required separator. It is shared by the hook and formatter so interactive
// flags and shell wrappers cannot be classified differently before and after
// execution.
func ExecCommand(args []string) ([]string, bool) {
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 1 || separator+1 >= len(args) {
		return nil, false
	}
	if HasFlag(args[:separator], "-i", "--stdin", "-t", "--tty") {
		return nil, false
	}
	return nestedcmd.Direct(args[separator+1:])
}
