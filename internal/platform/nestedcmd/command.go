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
	argv, ok := Direct(strings.Fields(joined))
	if !ok || filepath.Base(argv[0]) == "ssh" {
		return nil, false
	}
	return argv, true
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
		fields := strings.Fields(stage)
		if len(fields) == 0 || !narrowingTailPrograms[filepath.Base(fields[0])] {
			return nil, false
		}
	}
	return stages, true
}

// remotePipelineHead validates and returns the head stage's argv — the
// program whose formatter renders the (possibly truncated) combined output.
func remotePipelineHead(stages []string) ([]string, bool) {
	argv, ok := Direct(strings.Fields(stages[0]))
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
