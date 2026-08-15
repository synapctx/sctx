package run

import (
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/dockerargv"
	"github.com/synapctx/sctx/internal/platform/ghargv"
	"github.com/synapctx/sctx/internal/platform/gitargv"
	"github.com/synapctx/sctx/internal/platform/kubectlargv"
)

// Registry resolves a wrapped command's argv to the formatter that claims
// it. Adding support for a new tool means registering a new formatter here —
// the core pipeline never changes.
type Registry struct {
	formatters []registeredFormatter
}

type registeredFormatter struct {
	formatter format.Formatter
	project   bool
	override  bool
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(f format.Formatter) {
	r.formatters = append(r.formatters, registeredFormatter{formatter: f})
}

// RegisterProject adds a trusted project-local formatter. Built-ins remain
// authoritative unless the content-bound trusted rule explicitly opts into
// overriding one.
func (r *Registry) RegisterProject(f format.Formatter, overrideBuiltin bool) {
	r.formatters = append(r.formatters, registeredFormatter{
		formatter: f, project: true, override: overrideBuiltin,
	})
}

// ResolveByArgv picks the best-matching formatter for argv, or false when no
// formatter claims it (the chain then falls back to content sniffing and
// verbatim). Matching: program basename first, then Match.Subcommands as an
// ordered prefix of the non-flag arguments; longest subcommand match wins,
// then highest priority.
func (r *Registry) ResolveByArgv(argv []string) (format.Formatter, bool) {
	return r.resolveByArgv(argv, true)
}

// ResolveBuiltInByArgv excludes project-local formatters. Nested transports
// use it because trust granted for this checkout says nothing about a remote
// host, container filesystem, pod image, or their output grammar.
func (r *Registry) ResolveBuiltInByArgv(argv []string) (format.Formatter, bool) {
	return r.resolveByArgv(argv, false)
}

func (r *Registry) resolveByArgv(argv []string, allowProject bool) (format.Formatter, bool) {
	program, rest := normalize(argv)
	if program == "" {
		return nil, false
	}

	var builtin, project registeredFormatter
	builtinLen, builtinPri := -1, 0
	projectLen, projectPri := -1, 0
	for _, entry := range r.formatters {
		if entry.project && !allowProject {
			continue
		}
		m := entry.formatter.Descriptor()
		if m.Command != program {
			continue
		}
		if entry.project {
			specificity := len(m.Subcommands)
			if matcher, ok := entry.formatter.(interface {
				MatchesArgv([]string) bool
				MatchSpecificity() int
			}); ok {
				if !matcher.MatchesArgv(argv) {
					continue
				}
				specificity = matcher.MatchSpecificity()
			} else if !hasSubcommandPrefix(rest, m.Subcommands) {
				continue
			}
			if specificity > projectLen || (specificity == projectLen && m.Priority > projectPri) {
				project, projectLen, projectPri = entry, specificity, m.Priority
			}
			continue
		}
		if !hasSubcommandPrefix(rest, m.Subcommands) {
			continue
		}
		if len(m.Subcommands) > builtinLen || (len(m.Subcommands) == builtinLen && m.Priority > builtinPri) {
			builtin, builtinLen, builtinPri = entry, len(m.Subcommands), m.Priority
		}
	}
	if project.formatter != nil && (builtin.formatter == nil || project.override) {
		return project.formatter, true
	}
	return builtin.formatter, builtin.formatter != nil
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
	if program == "git" {
		inv, ok := gitargv.Parse(append([]string{program}, rest...))
		if !ok {
			return program
		}
		return program + " " + inv.Command
	}
	if program == "gh" {
		inv, ok := ghargv.Parse(append([]string{program}, rest...))
		if !ok {
			return program
		}
		key := program + " " + inv.Level1
		if inv.Level2 != "" {
			key += " " + inv.Level2
		}
		return key
	}
	if program == "kubectl" {
		inv, ok := kubectlargv.Parse(append([]string{program}, rest...))
		if !ok {
			return program
		}
		return program + " " + inv.Command
	}
	if program == "docker" {
		inv, ok := dockerargv.Parse(append([]string{program}, rest...))
		if !ok {
			return program
		}
		if inv.Command == "compose" {
			if nested, ok := dockerargv.ParseCompose(inv); ok {
				return program + " compose " + nested.Command
			}
		}
		if inv.Command == "network" || inv.Command == "volume" || inv.Command == "container" || inv.Command == "image" {
			for _, arg := range inv.Args {
				if !strings.HasPrefix(arg, "-") {
					return program + " " + inv.Command + " " + arg
				}
			}
		}
		return program + " " + inv.Command
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
