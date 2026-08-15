// Package nestedcmd defines the conservative command grammar shared by
// transport formatters that delegate output to an inner command formatter.
package nestedcmd

import (
	"path/filepath"
	"strings"
)

// Direct validates an argv that a transport executes without a shell.
// Shells choose the output grammar dynamically, while nested sctx would be a
// redundant wrapper, so neither can be delegated safely.
func Direct(argv []string) ([]string, bool) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return nil, false
	}
	base := filepath.Base(argv[0])
	if IsShell(base) || base == "sctx" {
		return nil, false
	}
	return append([]string(nil), argv...), true
}

// Remote joins the post-host tokens accepted by ssh and parses the deliberately
// narrow shell-like form that is safe to attribute to one executable. Quoted
// metacharacters also decline: losing an optimization is safer than guessing
// how a remote shell interpreted them.
func Remote(parts []string) ([]string, bool) {
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" || IsCompound(joined) {
		return nil, false
	}
	argv, ok := Direct(strings.Fields(joined))
	if !ok || filepath.Base(argv[0]) == "ssh" {
		return nil, false
	}
	return argv, true
}

// IsCompound identifies shell syntax that can combine commands, redirect
// bytes, or perform expansion before the transport invokes the program.
func IsCompound(s string) bool {
	return strings.ContainsAny(s, ";|&\n\r`") ||
		strings.Contains(s, "$(") || strings.ContainsAny(s, "><")
}

// IsShell reports programs whose output grammar is selected by a script rather
// than by the executable name visible in argv.
func IsShell(program string) bool {
	switch strings.ToLower(filepath.Base(program)) {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh",
		"cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh":
		return true
	default:
		return false
	}
}
