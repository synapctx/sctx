// Package dockerargv parses Docker's stable global argv and the nested
// `docker compose` command grammar.
//
// The hook, formatter, and stats key all use this package so a global option
// cannot make them disagree about which command the user invoked.
package dockerargv

import (
	"strings"

	"github.com/synapctx/sctx/internal/platform/nestedcmd"
)

// Invocation identifies a Docker command and the arguments after it.
type Invocation struct {
	Command      string
	CommandIndex int
	Args         []string
}

// globalValueFlags is derived from `docker --help` in Docker CLI v29.6.1.
var globalValueFlags = map[string]bool{
	"--config":    true,
	"--context":   true,
	"--host":      true,
	"--log-level": true,
	"--tlscacert": true,
	"--tlscert":   true,
	"--tlskey":    true,
	"-c":          true,
	"-H":          true,
	"-l":          true,
}

// composeValueFlags is derived from `docker compose --help` in Compose v5.3.0.
var composeValueFlags = map[string]bool{
	"--ansi":              true,
	"--env-file":          true,
	"--file":              true,
	"--parallel":          true,
	"--profile":           true,
	"--progress":          true,
	"--project-directory": true,
	"--project-name":      true,
	"-f":                  true,
	"-p":                  true,
}

// Parse locates Docker's top-level command after global options. argv includes
// argv[0]. Unknown global flags decline rather than guessing whether the next
// token is the command or a flag value.
func Parse(argv []string) (Invocation, bool) {
	return parse(argv, 1, globalValueFlags, isGlobalBool, true)
}

// ParseCompose locates the Compose command after `docker compose` and any
// Compose-level options. The supplied invocation must be returned by Parse.
func ParseCompose(inv Invocation) (Invocation, bool) {
	if inv.Command != "compose" {
		return Invocation{}, false
	}
	// Rebuild a small argv whose index zero represents `compose`. The resulting
	// index is translated back to the original Docker argv position.
	argv := append([]string{"compose"}, inv.Args...)
	nested, ok := parse(argv, 1, composeValueFlags, isComposeBool, false)
	if !ok {
		return Invocation{}, false
	}
	nested.CommandIndex += inv.CommandIndex
	return nested, true
}

func parse(argv []string, start int, values map[string]bool, isBool func(string) bool, attachedShort bool) (Invocation, bool) {
	if len(argv) <= start {
		return Invocation{}, false
	}
	for i := start; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return Invocation{}, false
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return Invocation{Command: a, CommandIndex: i, Args: argv[i+1:]}, true
		}
		name, hasAttached := flagName(a, attachedShort)
		if values[name] {
			if hasAttached {
				continue
			}
			if i+1 >= len(argv) {
				return Invocation{}, false
			}
			i++
			continue
		}
		if isBool(name) {
			continue
		}
		return Invocation{}, false
	}
	return Invocation{}, false
}

func flagName(arg string, attachedShort bool) (string, bool) {
	if before, _, ok := strings.Cut(arg, "="); ok {
		return before, true
	}
	if attachedShort && len(arg) > 2 && !strings.HasPrefix(arg, "--") {
		switch arg[:2] {
		case "-c", "-H", "-l":
			return arg[:2], true
		}
	}
	return arg, false
}

func isGlobalBool(name string) bool {
	switch name {
	case "--debug", "--tls", "--tlsverify", "--version", "-D", "-v":
		return true
	default:
		return false
	}
}

func isComposeBool(name string) bool {
	switch name {
	case "--all-resources", "--compatibility", "--dry-run":
		return true
	default:
		return false
	}
}

// OptionValue finds an option before a command separator. Long equals forms
// and attached one-letter shorthands are supported.
func OptionValue(args []string, names ...string) (string, bool) {
	for i, arg := range args {
		if arg == "--" {
			break
		}
		for _, name := range names {
			if arg == name {
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", false
			}
			if after, ok := strings.CutPrefix(arg, name+"="); ok {
				return after, true
			}
			if len(name) == 2 && strings.HasPrefix(name, "-") && !strings.HasPrefix(arg, "--") && strings.HasPrefix(arg, name) && len(arg) > 2 {
				return strings.TrimPrefix(arg, name), true
			}
		}
	}
	return "", false
}

// HasFlag reports whether a boolean option is enabled before a command
// separator. Explicit =false disables it and short clusters are recognized.
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

var execValueFlags = map[string]bool{
	"--detach-keys": true, "--env": true, "--env-file": true,
	"--user": true, "--workdir": true, "-e": true, "-u": true, "-w": true,
}

var composeExecValueFlags = map[string]bool{
	"--detach-keys": true, "--env": true, "--index": true,
	"--user": true, "--workdir": true, "-e": true, "-u": true, "-w": true,
}

// ExecCommand extracts a direct, finite inner command from docker exec or
// docker compose exec. Compose's interactive/TTY defaults must both be
// explicitly disabled. Shells and nested sctx calls are rejected.
func ExecCommand(command string, args []string) ([]string, bool) {
	compose := command == "compose exec"
	if command != "exec" && !compose {
		return nil, false
	}
	values := execValueFlags
	if compose {
		values = composeExecValueFlags
	}
	positional := -1
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = i + 1
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			name, attached := flagName(a, true)
			if !values[name] && len(a) > 2 && !strings.HasPrefix(a, "--") && values[a[:2]] {
				name, attached = a[:2], true
			}
			if values[name] {
				if !attached {
					if i+1 >= len(args) {
						return nil, false
					}
					i++
				}
				continue
			}
			if execBool(name, compose) {
				continue
			}
			return nil, false
		}
		positional = i
		break
	}
	if positional < 0 || positional+1 >= len(args) {
		return nil, false
	}
	options := args[:positional]
	if HasFlag(options, "-i", "--interactive", "-t", "--tty", "-d", "--detach") {
		return nil, false
	}
	// -T/--no-tty only: `docker compose exec --help` (Compose v5.7.1) has no
	// --interactive flag, so requiring --interactive=false made this path
	// unreachable. Attached non-TTY stdin is the agent's own, as at top level.
	if compose && !HasFlag(options, "-T", "--no-tty", "--no-TTY") {
		return nil, false
	}
	inner, ok := nestedcmd.Direct(args[positional+1:])
	if !ok {
		return nil, false
	}
	return inner, true
}

func execBool(name string, compose bool) bool {
	if compose {
		switch name {
		case "-d", "--detach", "--interactive", "--no-TTY", "--no-tty", "-T", "--privileged":
			return true
		}
		return false
	}
	switch name {
	case "-d", "--detach", "-i", "--interactive", "-t", "--tty", "--privileged":
		return true
	default:
		return false
	}
}

// runValueFlags are the value-taking options of `docker run` / `podman run`
// (derived from `docker run --help`, Docker CLI v29.9.3) that this grammar
// understands. An option outside runValueFlags and runBoolFlags declines the
// whole invocation: skipping an unknown flag could swallow the image or the
// command and attribute the output to the wrong program.
var runValueFlags = map[string]bool{
	"--add-host": true, "--cap-add": true, "--cap-drop": true, "--cpus": true,
	"--device": true, "--dns": true, "--entrypoint": true, "--env": true,
	"--env-file": true, "--expose": true, "--group-add": true, "--hostname": true,
	"--ipc": true, "--label": true, "--label-file": true, "--memory": true,
	"--mount": true, "--name": true, "--net": true, "--network": true,
	"--pid": true, "--platform": true, "--publish": true, "--pull": true,
	"--restart": true, "--security-opt": true, "--shm-size": true, "--tmpfs": true,
	"--ulimit": true, "--user": true, "--volume": true, "--volumes-from": true,
	"--workdir": true,
	"-e":        true, "-h": true, "-l": true, "-m": true, "-p": true, "-u": true,
	"-v": true, "-w": true,
}

// composeRunValueFlags are the value-taking options of `docker compose run`
// (Compose v5.7.1).
var composeRunValueFlags = map[string]bool{
	"--cap-add": true, "--cap-drop": true, "--entrypoint": true, "--env": true,
	"--env-from-file": true, "--label": true, "--name": true, "--publish": true,
	"--pull": true, "--user": true, "--volume": true, "--workdir": true,
	"-e": true, "-l": true, "-p": true, "-u": true, "-v": true, "-w": true,
}

func runBool(name string, compose bool) bool {
	switch name {
	case "--rm", "-q", "--quiet":
		return true
	case "--init", "--privileged", "--read-only", "-P", "--sig-proxy":
		return !compose
	case "-T", "--no-tty", "--no-TTY", "--no-deps", "--no-recreate", "--build", "--quiet-build",
		"--quiet-pull", "--service-ports", "--use-aliases", "--dry-run":
		return compose
	default:
		return false
	}
}

// RunCommand extracts the finite inner command of a non-interactive
// `docker run` / `podman run` (command "run") or `docker compose run`
// (command "compose run"). The inner command is everything after the image
// (service for compose); an --entrypoint value is prepended, because it
// replaces the image's own entrypoint as argv[0]. Anything it cannot parse
// with certainty, any -i/-t/-d/--attach, a shell, nested sctx, or an
// invocation that runs the image's default command (nothing after the image)
// declines. Compose additionally needs -T/--no-tty (spelled --no-TTY by older
// releases), as `compose run` allocates a TTY by default.
func RunCommand(command string, args []string) ([]string, bool) {
	compose := command == "compose run"
	if command != "run" && !compose {
		return nil, false
	}
	values := runValueFlags
	if compose {
		values = composeRunValueFlags
	}
	entrypoint, hasEntrypoint := "", false
	noTTY, stdinOff := false, false
	positional := -1
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = i + 1
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = i
			break
		}
		name, attached := flagName(a, false)
		val := ""
		if attached {
			_, val, _ = strings.Cut(a, "=")
		}
		// attached one-letter shorthand: -v/work:/w, -eKEY=1, -w/work
		if !values[name] && len(a) > 2 && !strings.HasPrefix(a, "--") && values[a[:2]] {
			name, attached, val = a[:2], true, a[2:]
		}
		switch {
		case values[name]:
			if !attached {
				if i+1 >= len(args) {
					return nil, false
				}
				i++
				val = args[i]
			}
			if name == "--entrypoint" {
				entrypoint, hasEntrypoint = val, true
			}
		case runBool(name, compose):
			if name == "-T" || name == "--no-tty" || name == "--no-TTY" {
				noTTY = !strings.EqualFold(val, "false")
			}
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 2 && runShortCluster(a[1:], compose, &noTTY):
		case isFalseFlag(a, "--interactive", "--tty", "--detach"):
			if isFalseFlag(a, "--interactive") {
				stdinOff = true
			}
		default:
			return nil, false
		}
	}
	if positional < 0 || positional >= len(args) {
		return nil, false
	}
	// `compose run` allocates a TTY by default, so -T/--no-tty is required. STDIN
	// staying attached (`-i, --interactive` defaults to true, Compose v5.7.1) is
	// NOT a reason to decline: it is the agent's own non-TTY stdin, the same one a
	// top-level wrapped command inherits, and a finite inner command finishes
	// with it attached. Requiring --interactive=false made the path dead in
	// practice (nobody types it), and `compose exec` has no such flag at all.
	_ = stdinOff
	if compose && !noTTY {
		return nil, false
	}
	rest := args[positional+1:] // after image / service
	var inner []string
	if hasEntrypoint {
		if strings.TrimSpace(entrypoint) == "" || strings.ContainsAny(entrypoint, " \t") {
			return nil, false
		}
		inner = append([]string{entrypoint}, rest...)
	} else {
		inner = rest
	}
	if len(inner) == 0 {
		return nil, false
	}
	return nestedcmd.Direct(inner)
}

// runShortCluster accepts a cluster of known, harmless one-letter booleans
// (-q, -P, and for compose -T). Anything else — notably -i, -t, -d, -a — is
// not a harmless boolean and the cluster is refused.
func runShortCluster(letters string, compose bool, noTTY *bool) bool {
	for _, c := range letters {
		switch {
		case c == 'q' || c == 'P':
		case c == 'T' && compose:
			*noTTY = true
		default:
			return false
		}
	}
	return true
}

// isFalseFlag reports an explicitly disabled long boolean (--tty=false).
func isFalseFlag(arg string, names ...string) bool {
	for _, n := range names {
		if after, ok := strings.CutPrefix(arg, n+"="); ok && strings.EqualFold(after, "false") {
			return true
		}
	}
	return false
}
