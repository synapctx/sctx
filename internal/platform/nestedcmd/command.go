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
//
// A pipeline is the one exception: `head | tail-stage...` is accepted when
// every stage after the head is one of narrowingTailPrograms — a program that
// only ever narrows/truncates the stream, never reorders, counts, or
// transforms it — so the combined captured output still faithfully matches
// what the HEAD program alone would have produced, just possibly truncated.
// Everything else that is compound (;, &, backticks, redirects,
// substitution, or a pipe into anything else) still declines outright.
func Remote(parts []string) ([]string, bool) {
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" {
		return nil, false
	}
	if stages, ok := splitNarrowingPipeline(joined); ok {
		return remotePipelineHead(stages)
	}
	if IsCompound(joined) {
		return nil, false
	}
	fields, ok := Split(joined)
	if !ok {
		return nil, false
	}
	argv, ok := Direct(fields)
	if !ok || filepath.Base(argv[0]) == "ssh" {
		return nil, false
	}
	return argv, true
}

// Split tokenizes one remote command line the way a POSIX shell would for the
// plain cases and declines (ok=false) for everything else: single and double
// quotes group words and are removed, but a backslash, a `$` or a backtick
// inside double quotes, or an unterminated quote means the remote shell would
// read something this grammar cannot confirm. strings.Fields used to split
// `sbt "~ test"` into `sbt`, `"~` and `test`, hiding the watch task.
func Split(s string) ([]string, bool) {
	var (
		out    []string
		cur    strings.Builder
		inWord bool
		quote  byte
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case quote == '"':
			switch c {
			case '"':
				quote = 0
			case '\\', '$', '`':
				return nil, false
			default:
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\':
			return nil, false
		case c == ' ' || c == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, true
}

// ShortCluster reports whether any single-dash argument (`-fn`, not `--long`)
// carries one of letters. Scanning a cluster stops at the first letter in
// valueLetters, because every character after a value-taking option is that
// option's VALUE (`journalctl -ufoo.service`, `-tfoo`): reading it as flags
// misreads `foo`'s `f` as --follow. A letter in both sets counts as a flag.
func ShortCluster(args []string, letters, valueLetters string) bool {
	for _, a := range args {
		if len(a) < 2 || a[0] != '-' || a[1] == '-' {
			continue
		}
		for _, c := range a[1:] {
			if strings.ContainsRune(letters, c) {
				return true
			}
			if strings.ContainsRune(valueLetters, c) {
				break
			}
		}
	}
	return false
}

// narrowingFollows reports a trailing pipeline stage that never reaches EOF:
// `ls | tail -f x` follows its stdin's closure forever for a file operand,
// and `less +F` follows too. The hook's streamsForever cannot be reached from
// here (hook imports this package), so the narrowing programs' own follow
// forms are recognised locally.
func narrowingFollows(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	args := argv[1:]
	switch filepath.Base(argv[0]) {
	case "tail":
		if ShortCluster(args, "fF", "ncs") {
			return true
		}
		for _, a := range args {
			if a == "--follow" || strings.HasPrefix(a, "--follow=") {
				return true
			}
		}
	case "less":
		for _, a := range args {
			if a == "+F" || a == "--follow-name" {
				return true
			}
		}
	}
	return false
}

// narrowingTailPrograms mirrors hook.pipeSafeDownstream: programs safe to
// trail a delegated pipeline because they only ever narrow/truncate the
// stream. Kept independent rather than imported — this policy also governs a
// REMOTE pipeline, which the hook's local table was never meant to reach.
var narrowingTailPrograms = map[string]bool{
	"head": true, "tail": true, "cat": true, "less": true, "more": true,
}

// splitNarrowingPipeline reports whether s is a pipeline (2+ stages joined by
// "|") whose stages after the first are ALL narrowing-tail programs with no
// nested compound syntax of their own. A bare "|" with no other compound
// character present is the only compound shape this function accepts; any
// other combination (a pipe alongside ";", "&", a redirect, backticks, or
// substitution) declines so a stage cannot smuggle a second effect through.
func splitNarrowingPipeline(s string) ([]string, bool) {
	if !strings.Contains(s, "|") {
		return nil, false
	}
	if strings.ContainsAny(s, ";&\n\r`") || strings.Contains(s, "$(") || strings.ContainsAny(s, "><") {
		return nil, false
	}
	stages := strings.Split(s, "|")
	if len(stages) < 2 {
		return nil, false
	}
	for i, stage := range stages {
		stage = strings.TrimSpace(stage)
		if stage == "" {
			return nil, false
		}
		stages[i] = stage
		if i == 0 {
			continue
		}
		fields, ok := Split(stage)
		if !ok || len(fields) == 0 || !narrowingTailPrograms[filepath.Base(fields[0])] || narrowingFollows(fields) {
			return nil, false
		}
	}
	return stages, true
}

// remotePipelineHead validates and returns the head stage's argv — the
// program whose formatter renders the (possibly truncated) combined output.
func remotePipelineHead(stages []string) ([]string, bool) {
	fields, ok := Split(stages[0])
	if !ok {
		return nil, false
	}
	argv, ok := Direct(fields)
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
