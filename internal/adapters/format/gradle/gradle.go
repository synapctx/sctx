// Package gradle implements a format.Formatter for Gradle (gradle, ./gradlew).
// A Gradle build prints one "> Task :x" line per task, a daemon/wrapper
// preamble, javac context lines for every warning, a "* Try:" help section and
// deprecation boilerplate. What a reader needs is the failed task, the
// compiler errors, each failing test with its exception and location, the
// "What went wrong" text, and the BUILD result with its task counts.
//
// Gradle already prints "N actionable tasks: ..." itself, so the per-task
// lines are dropped (counted) and only FAILED tasks survive. Compiler errors
// are echoed again inside "What went wrong"; that second copy is removed.
// The aggressive tier requires a BUILD SUCCESSFUL/FAILED footer.
package gradle

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders gradle/gradlew output.
type Formatter struct {
	command string
	getwd   func() string
}

// New returns the gradle formatter.
func New() format.Formatter { return newFormatter("gradle") }

// All returns the gradle and gradlew formatters. The registry matches the
// basename of argv[0], so ./gradlew needs its own entry.
func All() []format.Formatter {
	return []format.Formatter{newFormatter("gradle"), newFormatter("gradlew")}
}

func newFormatter(command string) *Formatter {
	return &Formatter{command: command, getwd: func() string {
		wd, _ := os.Getwd()
		return wd
	}}
}

// Descriptor claims every invocation of the program name.
func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: f.command}
}

var rawFlags = map[string]bool{
	"-i": true, "--info": true, "-d": true, "--debug": true,
	"-v": true, "--version": true, "-h": true, "--help": true, "-?": true,
}

// requestsRawOutput reports whether argv asks for log levels or banners the
// caller wants untouched.
func requestsRawOutput(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		if rawFlags[a] {
			return true
		}
	}
	return false
}

// Aggressive renders a task-collapsed, deduplicated build summary.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if requestsRawOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return f.aggressive(readAll(in.Stdout), readAll(in.Stderr))
}

// Relaxed drops wrapper download progress (counted) and collapses runs.
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

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// splitLines strips ANSI and carriage returns and splits on newlines.
func splitLines(raw []byte) []string {
	s := strings.TrimRight(ansiRE.ReplaceAllString(string(raw), ""), "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if j := strings.LastIndex(l, "\r"); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = l
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
	return func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "file://"+prefix, ""), prefix, "")
	}
}
