// Package pyfmt implements format.Formatters for the Python code formatters
// black and isort. Both write their reports to stderr and repeat an absolute
// path per file; the rendering states each path once (relative to the working
// directory), drops the banner, and keeps every file and every error.
package pyfmt

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// maxFiles caps the per-file list kept verbatim; the summary carries the true
// total and the cap is announced with an explicit "…+N more" marker.
const maxFiles = 40

type tool int

const (
	toolBlack tool = iota
	toolIsort
)

// Formatter renders black or isort output.
type Formatter struct {
	command string
	tool    tool
}

// New constructs the black formatter.
func New() *Formatter { return &Formatter{command: "black", tool: toolBlack} }

// NewIsort constructs the isort formatter.
func NewIsort() *Formatter { return &Formatter{command: "isort", tool: toolIsort} }

// All returns every formatter this package provides, for registration.
func All() []*Formatter { return []*Formatter{New(), NewIsort()} }

// Descriptor claims every invocation of the formatter's program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

// Aggressive renders the report; --diff output is the user's data and
// declines.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in)
}

// Relaxed shares the aggressive rendering: both formatters' reports are line
// oriented and fully understood, so there is no weaker heuristic to offer.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in)
}

func (f *Formatter) render(in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv[1:] {
		switch a {
		case "--diff", "-d", "--version", "--help", "-h", "--show-config", "--show-files":
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout := string(readAll(in.Stdout))
	stderr := string(readAll(in.Stderr))
	text := stderr + stdout
	if strings.TrimSpace(text) == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var body string
	var ok bool
	if f.tool == toolBlack {
		body, ok = renderBlack(text)
	} else {
		body, ok = renderIsort(text)
	}
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: stderr != ""}, nil
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

var getwd = os.Getwd

// relPath strips the working directory from an absolute path.
func relPath(p string) string {
	wd, err := getwd()
	if err != nil || wd == "" || !strings.HasPrefix(p, "/") {
		return p
	}
	if rest, ok := strings.CutPrefix(p, strings.TrimRight(wd, "/")+"/"); ok {
		return rest
	}
	return p
}

var (
	blackFileRe    = regexp.MustCompile(`^(would reformat|reformatted) (.+)$`)
	blackSummaryRe = regexp.MustCompile(`^(\d+ files? (?:would be |were )?(?:reformatted|left unchanged|would fail to reformat|failed to reformat)\b.*|No Python files are present to be formatted\..*)$`)
	blackBannerRe  = regexp.MustCompile(`^(All done! .*|Oh no! .*)$`)
)

// renderBlack handles `black`, `black --check` and the error shape
// ("error: cannot parse: ...").
func renderBlack(text string) (string, bool) {
	var files []string
	var verb string
	var other []string
	summary := ""
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		switch {
		case strings.TrimSpace(l) == "":
		case blackBannerRe.MatchString(l):
		case blackFileRe.MatchString(l):
			m := blackFileRe.FindStringSubmatch(l)
			verb = m[1]
			files = append(files, relPath(m[2]))
		case blackSummaryRe.MatchString(l):
			summary = l
		default:
			other = append(other, relPathInLine(l))
		}
	}
	if summary == "" {
		return "", false
	}
	var b strings.Builder
	for _, l := range other {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "%s:\n", verb)
		writeFiles(&b, files)
	}
	b.WriteString(summary)
	return b.String(), true
}

// relPathInLine rewrites an absolute cwd-prefixed path embedded in a line
// ("error: cannot parse: /abs/x.py:1:6").
func relPathInLine(l string) string {
	wd, err := getwd()
	if err != nil || wd == "" {
		return l
	}
	return strings.ReplaceAll(l, strings.TrimRight(wd, "/")+"/", "")
}

func writeFiles(b *strings.Builder, files []string) {
	shown := files
	if len(shown) > maxFiles {
		shown = shown[:maxFiles]
	}
	for _, f := range shown {
		b.WriteString("  ")
		b.WriteString(f)
		b.WriteByte('\n')
	}
	if more := len(files) - len(shown); more > 0 {
		fmt.Fprintf(b, "  …+%d more\n", more)
	}
}

var (
	isortErrRe   = regexp.MustCompile(`^ERROR: (.+?) Imports are incorrectly sorted and/or formatted\.$`)
	isortFixRe   = regexp.MustCompile(`^Fixing (.+)$`)
	isortSkipRe  = regexp.MustCompile(`^Skipped \d+ files?$`)
	isortUnsorts = "imports unsorted"
)

// renderIsort handles `isort --check-only`, `isort` (Fixing lines) and the
// Skipped counter. Anything else (config warnings, parse errors) is kept
// verbatim.
func renderIsort(text string) (string, bool) {
	var bad, fixed, other []string
	skipped := ""
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		switch {
		case strings.TrimSpace(l) == "":
		case isortErrRe.MatchString(l):
			bad = append(bad, relPath(isortErrRe.FindStringSubmatch(l)[1]))
		case isortFixRe.MatchString(l):
			fixed = append(fixed, relPath(isortFixRe.FindStringSubmatch(l)[1]))
		case isortSkipRe.MatchString(l):
			skipped = l
		default:
			other = append(other, relPathInLine(l))
		}
	}
	if len(bad) == 0 && len(fixed) == 0 && skipped == "" {
		return "", false
	}
	var b strings.Builder
	for _, l := range other {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(bad) > 0 {
		fmt.Fprintf(&b, "%s (%d):\n", isortUnsorts, len(bad))
		writeFiles(&b, bad)
	}
	if len(fixed) > 0 {
		fmt.Fprintf(&b, "fixed (%d):\n", len(fixed))
		writeFiles(&b, fixed)
	}
	if skipped != "" {
		b.WriteString(skipped)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), true
}
