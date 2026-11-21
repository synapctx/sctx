package run

import (
	"os"
	"path/filepath"
	"strings"
)

// childDefaults are set in the wrapped child's environment when the user has
// not chosen a value. Both are inert for programs that do not read them, which
// is what lets them apply to EVERY wrapped command: the interesting
// invocations are rarely the tool itself (`npm test`, `make e2e`,
// `turbo run e2e`, `make plan` all end up running Playwright or Terraform).
//
// PLAYWRIGHT_HTML_OPEN=never: Playwright's default html reporter has open:
// 'on-failure', which serves the report on a local web server and blocks until
// interrupted after a failing run. It skips that only when CI is set
// (lib/reporters/html.js onExit), not when there is no TTY, so a wrapped
// failing run would never reach EOF and sctx buffers stdout to EOF. CI is
// deliberately not set: it changes the behaviour of far more tools.
//
// TF_INPUT=0: Terraform asks for a missing variable on stdin, and the prompt
// is part of the output sctx buffers, so a wrapped plan would wait on a
// question nobody can see. With it, terraform fails fast with the usual "No
// value for required variable" error instead.
var childDefaults = []string{"PLAYWRIGHT_HTML_OPEN=never", "TF_INPUT=0"}

// childEnv returns the child's full environment: environ plus childDefaults for
// every name that is unset OR empty (tools read an empty value as unset). It
// never mutates environ and never touches sctx's own environment. nil means
// "inherit unchanged" — nothing to add.
//
// A command whose OUTPUT is the environment gets no overlay: `printenv TF_INPUT`
// must print nothing and exit 1 when the user never set it, and `env` must not
// list variables the user never set — the command's output and exit code are
// sacred.
func childEnv(argv, environ []string) []string {
	if inspectsEnvironment(argv) {
		return nil
	}
	var overlay []string
	for _, d := range childDefaults {
		name, _, _ := strings.Cut(d, "=")
		if !hasNonEmptyEnv(environ, name) {
			overlay = append(overlay, d)
		}
	}
	if len(overlay) == 0 {
		return nil
	}
	env := make([]string, 0, len(environ)+len(overlay))
	for _, kv := range environ {
		// Drop an empty entry for a name we are about to set, so the child
		// never sees the name twice.
		if name, v, _ := strings.Cut(kv, "="); v == "" && overridden(overlay, name) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, overlay...)
}

// inspectsEnvironment reports whether argv prints the environment: `printenv`
// in any form, or `env` with no command operand (only options and NAME=value
// assignments). `env FOO=1 make` runs make, so it does get the overlay.
func inspectsEnvironment(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch filepath.Base(argv[0]) {
	case "printenv":
		return true
	case "env":
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "-") || strings.Contains(a, "=") {
				continue
			}
			return false
		}
		return true
	}
	return false
}

func overridden(overlay []string, name string) bool {
	for _, d := range overlay {
		if n, _, _ := strings.Cut(d, "="); n == name {
			return true
		}
	}
	return false
}

// hasNonEmptyEnv reports whether environ sets name to a non-empty value.
func hasNonEmptyEnv(environ []string, name string) bool {
	for _, kv := range environ {
		if n, v, _ := strings.Cut(kv, "="); n == name && v != "" {
			return true
		}
	}
	return false
}

func processEnviron() []string { return os.Environ() }
