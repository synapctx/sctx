// Package minitest formats Minitest output as printed by `rake test`,
// `rails test` and `ruby -Itest file`: failures and errors with messages,
// diffs and project backtrace frames, the seed needed to reproduce, and the
// run summary. Progress rows, timing and rake's abort trailer are dropped.
package minitest

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders Minitest output for one entry point (rake, rails test or
// ruby). It holds no mutable state.
type Formatter struct{ match format.Match }

// New constructs the formatter for `rake` (rake test, rake, rake test:units).
func New() *Formatter { return &Formatter{format.Match{Command: "rake"}} }

// NewRails constructs the formatter for `rails test` / `bin/rails test`.
func NewRails() *Formatter {
	return &Formatter{format.Match{Command: "rails", Subcommands: []string{"test"}}}
}

// NewRuby constructs the formatter for `ruby -Itest test/x_test.rb`. It only
// applies when the output carries a Minitest summary.
func NewRuby() *Formatter { return &Formatter{format.Match{Command: "ruby"}} }

// Descriptor claims the entry point this Formatter was built for.
func (f *Formatter) Descriptor() format.Match { return f.match }

var (
	summaryRE  = regexp.MustCompile(`^\d+ (?:runs|tests), \d+ assertions, (\d+) failures, (\d+) errors, \d+ skips`)
	progressRE = regexp.MustCompile(`^[.FES]+$`)
	seedRE     = regexp.MustCompile(`^Run options:.*--seed (\d+)`)
	entryRE    = regexp.MustCompile(`^\s{0,3}\d+\) (Failure|Error|Skipped):$`)
	frameRE    = regexp.MustCompile(`^\s+\S+:\d+:in `)
	gemRE      = regexp.MustCompile(`/gems/|/vendor/bundle/|/lib/ruby/|/rubygems/|\(gem\)`)
	finishRE   = regexp.MustCompile(`^Finished in `)
	noiseRE    = regexp.MustCompile(`^(?:# Running:|You have skipped tests\.|Running \d+ tests? in |Started with run options)`)
)

// Aggressive renders failures and the summary.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(ansiRE.ReplaceAllString(stdout, ""))
	var summary string
	var failed bool
	for _, l := range lines {
		if m := summaryRE.FindStringSubmatch(l); m != nil {
			summary = l
			failed = m[1] != "0" || m[2] != "0"
		}
	}
	if summary == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var out []string
	seed := ""
	entries := 0
	gem := 0
	abort := 0
	inAbort := false
	flushGem := func() {
		if gem > 0 {
			out = append(out, fmt.Sprintf("    …+%d gem frames", gem))
			gem = 0
		}
	}
	for i, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		t := strings.TrimSpace(l)
		if inAbort {
			abort++
			continue
		}
		switch {
		case t == "rake aborted!" && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "Command failed with status"):
			// rake's own trailer for a failed test task; the failures above
			// already say why. Any other abort reason is kept.
			flushGem()
			inAbort = true
			abort = 1
			continue
		case t == "", progressRE.MatchString(t), finishRE.MatchString(t), noiseRE.MatchString(t):
			continue
		case summaryRE.MatchString(t):
			flushGem()
			continue // emitted last
		}
		if m := seedRE.FindStringSubmatch(t); m != nil {
			seed = m[1]
			continue
		}
		if entryRE.MatchString(l) {
			flushGem()
			entries++
			out = append(out, strings.TrimLeft(l, " "))
			continue
		}
		if frameRE.MatchString(l) {
			if gemRE.MatchString(l) {
				gem++
				continue
			}
			flushGem()
			out = append(out, "  "+t)
			continue
		}
		flushGem()
		out = append(out, l)
	}
	flushGem()
	if failed && entries == 0 {
		// The summary reports failures this parser did not find.
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for _, l := range out {
		b.WriteString(l + "\n")
	}
	if abort > 0 {
		fmt.Fprintf(&b, "…+%d rake abort lines\n", abort)
	}
	b.WriteString(summary)
	if seed != "" && failed {
		b.WriteString(" (seed " + seed + ")")
	}
	b.WriteByte('\n')
	if stderr != "" {
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != "", Elided: true}, nil
}

// Relaxed collapses blank lines, progress rows and repeats, but only for
// output that is recognisably Minitest: rake and ruby run arbitrary programs.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	if !strings.Contains(stdout, "# Running:") {
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
		return "", "", fmt.Errorf("minitest: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("minitest: reading stderr: %w", err)
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

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
