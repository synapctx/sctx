// Package fs formats the output of common filesystem inspection commands
// (ls, find, tree, env/printenv, df, wc, lsof) into token-minimal representations for AI agents.
package fs

import "github.com/synapctx/sctx/internal/domain/format"

// All returns the fs formatters: ls, find, tree, env, printenv, df, wc, lsof. Callers register each with
// the run registry.
func All() []format.Formatter {
	return []format.Formatter{
		&lsFormatter{},
		&findFormatter{},
		&treeFormatter{},
		&envFormatter{command: "env"},
		&envFormatter{command: "printenv"},
		&dfFormatter{},
		&wcFormatter{},
		&lsofFormatter{},
	}
}
