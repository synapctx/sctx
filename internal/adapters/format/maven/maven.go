// Package maven implements a format.Formatter for Apache Maven (mvn, ./mvnw,
// mvnd). Maven's log is mostly plumbing: per-artifact download progress,
// plugin banners, and a help footer that repeats on every failure. What a
// reader needs is the BUILD result, every [WARNING]/[ERROR] with its location,
// each failing test with its assertion and the application frame that threw,
// the test totals and the reactor summary.
//
// The aggressive tier keeps exactly those, collapses each module's plugin
// goals onto its header, drops duplicates (Maven prints every compiler error
// twice: once where it happens and again inside "Failed to execute goal") and
// ends with one marker line counting what was elided. It requires a
// BUILD SUCCESS/BUILD FAILURE footer, which also protects non-English locales:
// without one the tier is inapplicable rather than guessing.
package maven

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders mvn/mvnw/mvnd output.
type Formatter struct {
	command string
	getwd   func() string
}

// New returns the mvn formatter.
func New() format.Formatter { return newFormatter("mvn") }

// All returns the mvn, mvnw and mvnd formatters, sharing internals. The
// registry matches the basename of argv[0], so ./mvnw needs its own entry.
func All() []format.Formatter {
	return []format.Formatter{newFormatter("mvn"), newFormatter("mvnw"), newFormatter("mvnd")}
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

// verboseFlags ask Maven for detail the caller explicitly wants; rewriting
// that output would be answering a question nobody asked.
var verboseFlags = map[string]bool{"-X": true, "--debug": true}

// requestsRawOutput reports whether argv asks for output that must pass
// through untouched: debug logging, a version banner, or help.
func requestsRawOutput(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		if verboseFlags[a] || a == "-v" || a == "--version" || a == "-h" || a == "--help" {
			return true
		}
		// -Dmaven.ext.class.path etc. are fine; only an explicit
		// "-Dstyle.color=always" matters, and ANSI is stripped anyway.
	}
	return false
}

// Aggressive renders a structured, deduplicated build summary.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if requestsRawOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout := readAll(in.Stdout)
	stderr := readAll(in.Stderr)
	return f.aggressive(stdout, stderr)
}

// Relaxed removes only provably redundant content: download chatter (counted)
// and runs of identical lines.
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

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	// A progress frame optionally trails into the "Downloaded from" line that
	// completed it, separated only by padding spaces (Maven rewrites one
	// console line with \r).
	progressTailRE = regexp.MustCompile(`^Progress \(\d+\): .*?\s{2,}(Download(?:ing|ed) .*)$`)
	downloadRE     = regexp.MustCompile(`^(?:\[INFO\] )?Download(?:ing|ed) `)
)

// logLine is one logical console line after \r frames are separated.
type logLine struct {
	text     string
	download bool // a Downloading/Downloaded/Progress line, always dropped
}

// splitLog splits raw output into logical lines. Maven's transfer progress
// overwrites a single console line with \r; every frame becomes its own
// segment, and progress-only segments are classified as download noise.
func splitLog(raw []byte) (lines []logLine, progress int) {
	s := strings.TrimRight(ansiRE.ReplaceAllString(string(raw), ""), "\r\n")
	if s == "" {
		return nil, 0
	}
	for _, physical := range strings.Split(s, "\n") {
		physical = strings.TrimSuffix(physical, "\r")
		if !strings.Contains(physical, "\r") {
			lines = append(lines, classify(physical, &progress))
			continue
		}
		for seg := range strings.SplitSeq(physical, "\r") {
			if strings.TrimSpace(seg) == "" {
				continue
			}
			lines = append(lines, classify(seg, &progress))
		}
	}
	return lines, progress
}

func classify(seg string, progress *int) logLine {
	if m := progressTailRE.FindStringSubmatch(seg); m != nil {
		*progress++
		return logLine{text: m[1], download: true}
	}
	if strings.HasPrefix(seg, "Progress (") {
		*progress++
		return logLine{text: seg, download: true}
	}
	if downloadRE.MatchString(seg) {
		return logLine{text: seg, download: true}
	}
	return logLine{text: seg}
}

// relativizer strips the working directory from paths so a compiler error
// reads "src/main/java/demo/Calc.java:[3,47]" instead of repeating a 120
// character absolute prefix on every line.
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

func joinBody(lines []string) []byte {
	return []byte(strings.Join(lines, "\n"))
}
