// Package dotnet implements a format.Formatter for the .NET SDK CLI (dotnet
// build, test, restore, publish, pack, format). MSBuild prints every
// diagnostic twice — once as it is hit and again in the summary after "Build
// FAILED." — repeats the same warning code dozens of times, and wraps the
// result in restore chatter. The aggressive tier keeps each error with its
// file(line,col), groups warnings by code with a count, shows each failing
// test's message and the user-code stack frames, and merges the result block
// ("Build FAILED.", "N Warning(s)", "N Error(s)", "Time Elapsed") into one line.
//
// `dotnet run` and `dotnet watch` are NOT claimed by the hook (the program's
// own output, possibly a server that never exits); they are not formatted here.
package dotnet

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders dotnet output.
type Formatter struct {
	getwd func() string
}

// New returns the dotnet formatter.
func New() format.Formatter {
	return &Formatter{getwd: func() string {
		wd, _ := os.Getwd()
		return wd
	}}
}

// Descriptor claims every dotnet invocation; Aggressive decides by output.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: "dotnet"}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// requestsRawOutput reports whether argv selects a log shape the caller
// asked for explicitly: diagnostic verbosity, the interactive terminal
// logger, or MSBuild's machine-readable -get* queries.
func requestsRawOutput(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := strings.ToLower(argv[i])
		switch {
		case a == "-h" || a == "--help" || a == "-?" || a == "--version" || a == "--info" ||
			a == "--list-sdks" || a == "--list-runtimes":
			return true
		case strings.HasPrefix(a, "-tl") || strings.HasPrefix(a, "--tl") || strings.HasPrefix(a, "-terminallogger") ||
			strings.HasPrefix(a, "--terminallogger"):
			return true
		case strings.HasPrefix(a, "-getproperty") || strings.HasPrefix(a, "-getitem") ||
			strings.HasPrefix(a, "-gettargetresult") || strings.HasPrefix(a, "-getresultoutputfile"):
			return true
		case a == "-v" || a == "--verbosity" || a == "-verbosity":
			if i+1 < len(argv) && verboseLevel(argv[i+1]) {
				return true
			}
		case strings.HasPrefix(a, "-v:") || strings.HasPrefix(a, "--verbosity:") || strings.HasPrefix(a, "--verbosity="):
			if verboseLevel(a[strings.IndexAny(a, ":=")+1:]) {
				return true
			}
		}
	}
	return false
}

func verboseLevel(v string) bool {
	switch strings.ToLower(v) {
	case "d", "detailed", "diag", "diagnostic":
		return true
	}
	return false
}

// Aggressive renders the grouped build/test summary.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if requestsRawOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return f.aggressive(in.Argv, readAll(in.Stdout), readAll(in.Stderr))
}

// Relaxed drops restore chatter (counted) and collapses runs of identical lines.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if requestsRawOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return relaxed(readAll(in.Stdout), readAll(in.Stderr))
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	s := strings.TrimRight(ansiRE.ReplaceAllString(string(raw), ""), "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

func (f *Formatter) relativizer() func(string) string {
	wd := ""
	if f.getwd != nil {
		wd = f.getwd()
	}
	if wd == "" || wd == "/" {
		return func(s string) string { return s }
	}
	prefix := strings.TrimSuffix(wd, "/") + "/"
	return func(s string) string { return strings.ReplaceAll(s, prefix, "") }
}
