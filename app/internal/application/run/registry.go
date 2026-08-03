package run

import (
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Registry resolves a wrapped command's argv to the formatter that claims
// it. Adding support for a new tool means registering a new formatter here —
// the core pipeline never changes.
type Registry struct {
	formatters []format.Formatter
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(f format.Formatter) {
	r.formatters = append(r.formatters, f)
}

// ResolveByArgv picks the best-matching formatter for argv, or false when no
// formatter claims it (the chain then falls back to content sniffing and
// verbatim). Matching: program basename first, then Match.Subcommands as an
// ordered prefix of the non-flag arguments; longest subcommand match wins,
// then highest priority.
func (r *Registry) ResolveByArgv(argv []string) (format.Formatter, bool) {
	program, rest := normalize(argv)
	if program == "" {
		return nil, false
	}

	var best format.Formatter
	bestLen, bestPri := -1, 0
	for _, f := range r.formatters {
		m := f.Descriptor()
		if m.Command != program {
			continue
		}
		if !hasSubcommandPrefix(rest, m.Subcommands) {
			continue
		}
		if len(m.Subcommands) > bestLen || (len(m.Subcommands) == bestLen && m.Priority > bestPri) {
			best, bestLen, bestPri = f, len(m.Subcommands), m.Priority
		}
	}
	return best, best != nil
}

// subcommandPrograms lists programs whose first non-flag argument is a
// subcommand worth including in the stats key. For anything else the key is
// the bare program name — including arbitrary arguments (grep patterns, file
// paths) would explode stats cardinality.
var subcommandPrograms = map[string]bool{
	"go": true, "git": true, "docker": true, "kubectl": true, "helm": true,
	"npm": true, "pnpm": true, "yarn": true, "cargo": true, "gh": true,
	"ruff": true, "brew": true, "pip": true, "pip3": true,
}

// valueFlags lists per-program global flags that consume the next argument,
// so it is not mistaken for the subcommand (e.g. `git -C path status`).
var valueFlags = map[string]map[string]bool{
	"git":     {"-C": true, "-c": true, "--git-dir": true, "--work-tree": true},
	"docker":  {"--context": true, "-H": true, "--host": true},
	"kubectl": {"--context": true, "-n": true, "--namespace": true, "--kubeconfig": true},
}

// CommandKey returns the normalized stats key for argv: the program basename
// plus its subcommand for known multi-command tools, e.g. "go test",
// "git status", plain "grep" otherwise.
func CommandKey(argv []string) string {
	program, rest := normalize(argv)
	if program == "" {
		return ""
	}
	if !subcommandPrograms[program] {
		return program
	}
	skipNext := false
	for _, a := range rest {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			if valueFlags[program][a] && !strings.Contains(a, "=") {
				skipNext = true
			}
			continue
		}
		return program + " " + a
	}
	return program
}

// normalize strips leading VAR=val environment assignments and returns the
// program basename plus the remaining arguments.
func normalize(argv []string) (string, []string) {
	i := 0
	for i < len(argv) && isEnvAssignment(argv[i]) {
		i++
	}
	if i >= len(argv) {
		return "", nil
	}
	return filepath.Base(argv[i]), argv[i+1:]
}

func isEnvAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range s[:eq] {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// hasSubcommandPrefix reports whether want is an ordered prefix of the
// non-flag arguments in args.
func hasSubcommandPrefix(args, want []string) bool {
	if len(want) == 0 {
		return true
	}
	i := 0
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a != want[i] {
			return false
		}
		i++
		if i == len(want) {
			return true
		}
	}
	return false
}
