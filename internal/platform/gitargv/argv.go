// Package gitargv parses Git's stable global argv and classifies invocations
// that cannot safely be buffered by sctx.
package gitargv

import "strings"

// Invocation identifies Git's command and the arguments after it.
type Invocation struct {
	Command      string
	CommandIndex int
	Args         []string
}

var globalValueFlags = map[string]bool{
	"-C": true, "-c": true,
	"--git-dir": true, "--work-tree": true, "--namespace": true,
	"--super-prefix": true, "--config-env": true,
}

// Parse locates Git's command after global options. Unknown global options
// decline instead of guessing whether their next token is a value.
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
		if isKnownBooleanGlobal(name) || isKnownAttachedOnlyGlobal(name, attached) {
			continue
		}
		return Invocation{}, false
	}
	return Invocation{}, false
}

func flagName(arg string) (string, bool) {
	if before, _, ok := strings.Cut(arg, "="); ok {
		return before, true
	}
	return arg, false
}

func isKnownBooleanGlobal(name string) bool {
	switch name {
	case "--no-pager", "--paginate", "-p", "-P", "--no-replace-objects",
		"--bare", "--no-optional-locks", "--no-advice", "--literal-pathspecs",
		"--glob-pathspecs", "--noglob-pathspecs", "--icase-pathspecs":
		return true
	default:
		return false
	}
}

func isKnownAttachedOnlyGlobal(name string, attached bool) bool {
	if !attached {
		return false
	}
	switch name {
	case "--exec-path", "--list-cmds", "--attr-source":
		return true
	default:
		return false
	}
}

// HasFlag reports whether a command-local option is present before --.
// Long equals forms and one-letter short clusters are recognized.
func HasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		for _, name := range names {
			if arg == name {
				return true
			}
			if strings.HasPrefix(arg, name+"=") {
				return !strings.EqualFold(strings.TrimPrefix(arg, name+"="), "false")
			}
			if len(name) == 2 && strings.HasPrefix(name, "-") && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 2 && strings.ContainsRune(arg[1:], rune(name[1])) {
				return true
			}
		}
	}
	return false
}

// HasOptionPrefix reports options whose value may be attached with '='.
func HasOptionPrefix(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

// SafeToBuffer rejects commands that stream, run an interactive UI/editor,
// or wait for a protocol on stdin. All other Git commands are finite and may
// reach the formatter's structured/relaxed/verbatim tier chain.
func SafeToBuffer(inv Invocation) bool {
	switch inv.Command {
	case "daemon", "credential", "credential-cache--daemon", "fast-import",
		"shell", "receive-pack", "upload-pack", "upload-archive", "imap-send":
		return false
	case "add":
		return !HasFlag(inv.Args, "-p", "--patch", "-i", "--interactive", "-e", "--edit")
	case "commit":
		// Without a message source Git opens an editor.
		return HasFlag(inv.Args, "-m", "--message", "-F", "--file", "-C", "--reuse-message", "--no-edit")
	case "checkout", "restore", "reset", "stash":
		return !HasFlag(inv.Args, "-p", "--patch")
	case "clean":
		return !HasFlag(inv.Args, "-i", "--interactive")
	case "config":
		return !HasFlag(inv.Args, "-e", "--edit")
	case "notes":
		if containsArg(inv.Args, "edit") || HasFlag(inv.Args, "--stdin") {
			return false
		}
		if containsArg(inv.Args, "add") || containsArg(inv.Args, "append") {
			return HasFlag(inv.Args, "-m", "--message", "-F", "--file", "-C", "--reuse-message")
		}
		return true
	case "tag":
		if HasFlag(inv.Args, "-a", "--annotate", "-s", "--sign", "-u", "--local-user") {
			return HasFlag(inv.Args, "-m", "--message", "-F", "--file")
		}
		return true
	case "rebase":
		return !HasFlag(inv.Args, "-i", "--interactive", "--edit-todo")
	case "difftool", "mergetool":
		return HasFlag(inv.Args, "-y", "--no-prompt")
	case "cat-file":
		return !HasOptionPrefix(inv.Args, "--batch", "--batch-check", "--batch-command")
	case "fsmonitor--daemon":
		return len(inv.Args) > 0 && inv.Args[0] != "run"
	case "send-email", "gui", "citool":
		return false
	default:
		return !HasFlag(inv.Args, "--interactive", "--open-files-in-pager")
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
