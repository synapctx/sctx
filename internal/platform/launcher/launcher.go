// Package launcher recognises the package-runner and interpreter launchers
// (`npx tsc`, `uv run pytest`, `bundle exec rspec`, `python -m mypy`) whose OUTPUT is the inner
// tool's, so both the rewrite hook and the run pipeline attribute a launched
// command to the tool it runs.
//
// The grammar is deliberately narrow and shared: the hook decides what to
// wrap with it and the registry decides which formatter claims the result, so
// the two cannot disagree. Anything with a launcher flag this package does not
// know is NOT peeled — a flag may consume the next argument, and guessing
// would name the wrong program.
package launcher

import (
	"path/filepath"
	"regexp"
	"strings"
)

var pythonProgram = regexp.MustCompile(`^python(3(\.[0-9]+)?)?$`)

// pythonModules are the only `python -m` modules peeled: each has its own
// formatter or table row. `python -m http.server` and friends are user code.
var pythonModules = map[string]bool{
	"pytest": true, "mypy": true, "ruff": true, "pip": true,
	"unittest": true, "black": true, "isort": true, "pylint": true, "flake8": true,
	"pyright": true, "sqlfluff": true,
}

// runnerFlags are the boolean flags of npx/bunx that never take a value.
var runnerFlags = map[string]bool{
	"-y": true, "--yes": true, "--no": true, "--no-install": true,
	"-q": true, "--quiet": true, "--bun": true,
}

// Peel reports the index in argv of the inner program a launcher runs, and the
// inner program's name with any `@version` suffix removed. argv[0] is the
// launcher; ok is false for anything that is not an unambiguous launcher form.
// Only one level is peeled.
func Peel(argv []string) (idx int, name string, ok bool) {
	if len(argv) < 2 {
		return 0, "", false
	}
	switch base := filepath.Base(argv[0]); {
	case base == "npx" || base == "bunx":
		i := 1
		for i < len(argv) && runnerFlags[argv[i]] {
			i++
		}
		return inner(argv, i)
	case base == "pnpm":
		if argv[1] == "dlx" || argv[1] == "exec" {
			return inner(argv, 2)
		}
	case base == "npm":
		if argv[1] == "exec" {
			i := 2
			if i < len(argv) && argv[i] == "--" {
				i++
			}
			return inner(argv, i)
		}
	case base == "uv" || base == "poetry":
		if argv[1] == "run" {
			return inner(argv, 2)
		}
	case base == "bundle":
		if argv[1] == "exec" {
			return inner(argv, 2)
		}
	case pythonProgram.MatchString(base):
		i := 1
		if argv[i] == "-u" {
			i++
		}
		if i+1 < len(argv) && argv[i] == "-m" && pythonModules[argv[i+1]] {
			return i + 1, argv[i+1], true
		}
	}
	return 0, "", false
}

// Inner returns argv with the launcher removed: the inner program first,
// normalised, then its arguments. It returns argv unchanged when argv is not a
// launcher form.
//
// `bundle exec` is the one launcher Peel reports that Inner does not remove: the
// bundler formatter owns `bundle` output and delegates to the inner tool's
// formatter itself, so the registry must keep resolving it as `bundle`. The hook
// still peels it, to apply the inner tool's row and streaming rules.
func Inner(argv []string) []string {
	idx, name, ok := Peel(argv)
	if !ok || filepath.Base(argv[0]) == "bundle" {
		return argv
	}
	out := make([]string, 0, len(argv)-idx)
	out = append(out, name)
	return append(out, argv[idx+1:]...)
}

// attributable are the inner tools a launched command may be NAMED after in
// telemetry and stats keys: tools sctx has a formatter or rewrite-table row
// for. Anything else a launcher runs (`uv run scripts/sync_acme_billing.py`,
// `npx @acme/internal-cli`, `pnpm exec acme-deploy`) is a script or a possibly
// private package, so the key stays on the launcher. Formatter resolution is
// not restricted by this set; it never leaves the machine.
var attributable = map[string]bool{
	"tsc": true, "eslint": true, "jest": true, "vitest": true,
	"pytest": true, "mypy": true, "ruff": true, "pip": true, "pip3": true,
	"black": true, "isort": true, "flake8": true, "pylint": true, "pyright": true,
	"basedpyright": true, "ty": true, "sqlfluff": true, "unittest": true,
	"pdm": true, "pip-compile": true,
	"playwright": true, "prettier": true, "prisma": true, "turbo": true, "nx": true,
	"biome": true, "oxlint": true, "next": true,
}

// Attributed is Inner restricted to attributable inner tools: the argv whose
// program name is safe to put in a telemetry or stats key. A launcher running
// anything else is returned unchanged, so it keys on the launcher itself.
func Attributed(argv []string) []string {
	if _, name, ok := Peel(argv); !ok || !attributable[name] || filepath.Base(argv[0]) == "bundle" {
		return argv
	}
	return Inner(argv)
}

func inner(argv []string, i int) (int, string, bool) {
	if i >= len(argv) {
		return 0, "", false
	}
	tool := argv[i]
	if tool == "" || strings.HasPrefix(tool, "-") || strings.ContainsAny(tool, "\"'$`") {
		return 0, "", false
	}
	// `tsc@5.4` names a version, not a different program. A scoped package
	// (`@scope/tool`) starts with '@' and is left alone: its binary name is not
	// recoverable from the package name.
	if at := strings.Index(tool, "@"); at > 0 {
		tool = tool[:at]
	}
	return i, tool, true
}
