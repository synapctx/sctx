// Package ghargv parses GitHub CLI's stable global argv and classifies the
// read-only command surface that sctx can safely buffer.
package ghargv

import "strings"

// Invocation identifies a top-level and optional nested gh command.
type Invocation struct {
	Level1       string
	Level2       string
	CommandIndex int
	NestedIndex  int
	Args         []string
}

var globalValueFlags = map[string]bool{
	"-R": true, "--repo": true, "--hostname": true,
}

var nestedFamilies = map[string]bool{
	"pr": true, "issue": true, "run": true, "repo": true, "release": true,
}

// Parse locates gh's command after persistent flags. Unknown pre-command
// flags decline instead of guessing whether the following token is a value.
func Parse(argv []string) (Invocation, bool) {
	if len(argv) < 2 {
		return Invocation{}, false
	}
	l1Index, ok := scanCommand(argv, 1)
	if !ok {
		return Invocation{}, false
	}
	inv := Invocation{Level1: argv[l1Index], CommandIndex: l1Index, NestedIndex: -1}
	if !nestedFamilies[inv.Level1] {
		inv.Args = argv[l1Index+1:]
		return inv, true
	}
	l2Index, ok := scanCommand(argv, l1Index+1)
	if !ok {
		return Invocation{}, false
	}
	inv.Level2 = argv[l2Index]
	inv.NestedIndex = l2Index
	inv.Args = argv[l2Index+1:]
	return inv, true
}

func scanCommand(argv []string, start int) (int, bool) {
	for i := start; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return 0, false
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return i, true
		}
		name, attached := flagName(a)
		if globalValueFlags[name] {
			if attached {
				continue
			}
			if i+1 >= len(argv) {
				return 0, false
			}
			i++
			continue
		}
		if name == "--help" || name == "--version" {
			continue
		}
		return 0, false
	}
	return 0, false
}

func flagName(arg string) (string, bool) {
	if before, _, ok := strings.Cut(arg, "="); ok {
		return before, true
	}
	if len(arg) > 2 && strings.HasPrefix(arg, "-R") && !strings.HasPrefix(arg, "--") {
		return "-R", true
	}
	return arg, false
}

// HasFlag reports whether a boolean option is enabled before --.
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
		}
	}
	return false
}

// HasOption reports whether an option is present in separate or equals form.
func HasOption(args []string, names ...string) bool {
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

// SafeReadOnly reports the finite, non-interactive gh surface for which sctx
// has dedicated or shape-only coverage. Mutating and prompt-driven commands
// remain native; their usually short output offers no worthwhile saving.
func SafeReadOnly(inv Invocation) bool {
	if HasFlag(inv.Args, "--web") {
		return false
	}
	switch inv.Level1 + " " + inv.Level2 {
	case "pr list", "pr view", "pr status", "pr diff":
		return true
	case "pr checks":
		return !HasFlag(inv.Args, "--watch")
	case "issue list", "issue view", "issue status":
		return true
	case "run list", "run view":
		return true
	case "run watch":
		return false
	case "repo list", "repo view", "release list", "release view":
		return true
	}
	if inv.Level1 == "api" {
		return !HasOption(inv.Args, "--input") || !optionIsStdin(inv.Args, "--input")
	}
	return false
}

func optionIsStdin(args []string, name string) bool {
	for i, arg := range args {
		if arg == name {
			return i+1 < len(args) && args[i+1] == "-"
		}
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=") == "-"
		}
	}
	return false
}
