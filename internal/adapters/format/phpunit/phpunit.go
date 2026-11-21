// Package phpunit formats PHPUnit (9 through 13) text output: each error,
// failure, warning and risky test with its assertion message, diff and
// file:line frames, paths made relative to the project, and the run summary.
// The banner, runtime/configuration header, progress row and timing are dropped.
package phpunit

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `phpunit` output. It holds no state.
type Formatter struct{}

// New constructs a Formatter for `phpunit` (vendor/bin/phpunit, phpunit).
func New() *Formatter { return &Formatter{} }

// Descriptor claims every `phpunit` invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "phpunit"} }

var (
	okButRE    = regexp.MustCompile(`^OK, but .*!$`)
	testsRE    = regexp.MustCompile(`^Tests: \d+, Assertions: \d+`)
	verdictRE  = regexp.MustCompile(`^(?:OK \(\d+ tests?, \d+ assertions?\)|No tests executed!)$`)
	failVerdRE = regexp.MustCompile(`^(?:FAILURES|ERRORS|WARNINGS|RISKY|DEPRECATIONS|NOTICES)!$`)
	progressRE = regexp.MustCompile(`^[.FEWSIRD]+(?:\s+\d+ / \d+ \(\s*\d+%\))?\s*$`)
	headerRE   = regexp.MustCompile(`^(?:PHPUnit \d+\.\d+\.\d+ by |Runtime:|Configuration:|Random Seed:|Time: )`)
	sectionRE  = regexp.MustCompile(`^There (?:was|were) (\d+) (.+):$`)
	configRE   = regexp.MustCompile(`^Configuration:\s+(.+)/[^/]+$`)
	passedRE   = regexp.MustCompile(`^\s*✔ `)
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	entryRE    = regexp.MustCompile(`^\d+\) \S`)
)

// Aggressive renders failures first and the summary last.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv {
		if a == "--teamcity" || a == "--testdox-xml" || strings.HasPrefix(a, "--log-teamcity") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(ansiRE.ReplaceAllString(stdout, ""))

	hasSummary := false
	failed := false
	for _, l := range lines {
		switch {
		case testsRE.MatchString(l), verdictRE.MatchString(l):
			hasSummary = true
			if strings.Contains(l, "Failures:") || strings.Contains(l, "Errors:") {
				failed = true
			}
		}
	}
	if !hasSummary {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	roots := projectRoots(lines)
	rel := func(s string) string {
		for _, r := range roots {
			s = strings.ReplaceAll(s, r+"/", "")
		}
		return s
	}

	var out []string
	entries := 0
	dropped := 0
	passed := 0
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case l == "" || t == "--":
			continue
		case headerRE.MatchString(t), progressRE.MatchString(l), t == "│":
			dropped++
			continue
		case passedRE.MatchString(l):
			passed++
			continue
		case okButRE.MatchString(t):
			dropped++ // "OK, but some tests were skipped!": the Tests: line carries the counts
			continue
		case failVerdRE.MatchString(t):
			// Verdict word joins the counts line below.
			if i+1 < len(lines) && testsRE.MatchString(lines[i+1]) {
				out = append(out, t+" "+lines[i+1])
				i++
				continue
			}
		case testsRE.MatchString(t):
			out = append(out, "Tests: "+strings.TrimPrefix(t, "Tests: "))
			continue
		}
		if m := sectionRE.FindStringSubmatch(t); m != nil {
			out = append(out, m[1]+" "+m[2]+":")
			continue
		}
		if entryRE.MatchString(l) {
			entries++
		}
		out = append(out, rel(strings.TrimRight(l, " \t")))
	}
	if failed && entries == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for _, l := range out {
		b.WriteString(l + "\n")
	}
	if dropped+passed > 0 {
		n := dropped + passed
		fmt.Fprintf(&b, "…+%d lines (banner, progress", n)
		if passed > 0 {
			fmt.Fprintf(&b, ", %d passing tests", passed)
		}
		b.WriteString(")\n")
	}
	if stderr != "" {
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != "", Elided: true}, nil
}

// projectRoots returns directory prefixes to strip from absolute paths: the
// directory holding the phpunit configuration, then the working directory.
func projectRoots(lines []string) []string {
	var roots []string
	for _, l := range lines {
		if m := configRE.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			roots = append(roots, m[1])
			break
		}
	}
	if wd, err := os.Getwd(); err == nil && wd != "/" && wd != "" {
		roots = append(roots, strings.TrimSuffix(wd, "/"))
	}
	return roots
}

// Relaxed drops blank lines and progress rows, collapsing repeats.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	for _, a := range in.Argv {
		if a == "--teamcity" || a == "--testdox-xml" || strings.HasPrefix(a, "--log-teamcity") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if stdout == "" && stderr == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	for _, text := range []string{ansiRE.ReplaceAllString(stdout, ""), ansiRE.ReplaceAllString(stderr, "")} {
		lines := splitLines(text)
		for i := 0; i < len(lines); {
			l := strings.TrimRight(lines[i], " \t")
			if l == "" || progressRE.MatchString(l) {
				i++
				continue
			}
			j := i + 1
			for j < len(lines) && strings.TrimRight(lines[j], " \t") == l {
				j++
			}
			b.WriteString(l)
			if n := j - i; n > 1 {
				fmt.Fprintf(&b, " ×%d", n)
			}
			b.WriteByte('\n')
			i = j
		}
	}
	if b.Len() == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != ""}, nil
}

func readStreams(in format.Input) (stdout, stderr string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return "", "", fmt.Errorf("phpunit: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("phpunit: reading stderr: %w", err)
	}
	return string(o), string(e), nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
