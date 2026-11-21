// Package bazel formats `bazel build` and `bazel test` (and bazelisk). With no
// terminal, Bazel prints a fresh progress row for every phase ("Computing main
// repo mapping:", "Loading:", "Analyzing: N targets (...)", "[3 / 3] no actions
// running"); those, the server start-up banner and the advice trailers are
// dropped. Errors keep their location and cause but lose the embedded command
// line; test verdict rows, failure log paths and the build verdict are kept.
package bazel

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders one launcher's output (bazel or bazelisk). It holds no state.
type Formatter struct{ command string }

// All returns the bazel and bazelisk formatters.
func All() []format.Formatter {
	return []format.Formatter{&Formatter{command: "bazel"}, &Formatter{command: "bazelisk"}}
}

// Descriptor claims every invocation of the launcher.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	progressRE = regexp.MustCompile(`^(?:Computing main repo mapping:|Loading:|Analyzing:|Checking cached actions|Fetching |\[\d[\d,]* / \d[\d,]*\]|Building:|Extracting Bazel installation|Starting local Bazel server|\(\d+:\d+:\d+\) )`)
	infoNoise  = regexp.MustCompile(`^INFO: (?:Analyzed |Found \d+ targets?|\d+ processes?:|Repository \S+ instantiated|From |Invocation ID|Streaming build results|Build Event Protocol)`)
	adviceRE   = regexp.MustCompile(`^(?:Use --(?:sandbox_debug|verbose_failures)|There were tests whose specified size|Executing tests from)`)
	cmdTailRE  = regexp.MustCompile(`^((?:ERROR|FAILED): .*\(from target \S+?\)) \S.*$`)
	logPathRE  = regexp.MustCompile(`^\s+(/\S+/test\.log)$`)
)

// applicable reports whether the first non-flag word is build or test.
func applicable(argv []string) bool {
	for _, a := range argv[1:] {
		if a == "--help" || a == "-h" || strings.HasPrefix(a, "--curses=yes") {
			return false
		}
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "build" || a == "test"
	}
	return false
}

// Aggressive drops progress, banners, advice and command-line tails.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only progress rows and blank lines.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !applicable(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("bazel: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("bazel: reading stderr: %w", err)
	}
	// Bazel's progress and diagnostics go to stderr; the verdict table to stdout.
	text := ansiRE.ReplaceAllString(string(rawErr)+string(rawOut), "")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped, chars := filter(strings.Split(text, "\n"), aggressive)
	if dropped == 0 && chars == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("…+%d lines (progress rows, banners, advice, blanks)", dropped))
	}
	if chars > 0 {
		kept = append(kept, fmt.Sprintf("…+%d chars (embedded command lines)", chars))
	}
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, aggressive bool) (kept []string, dropped, chars int) {
	seenLog := map[string]bool{}
	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		switch {
		case l == "", progressRE.MatchString(l):
			dropped++
			continue
		}
		if !aggressive {
			kept = append(kept, l)
			continue
		}
		switch {
		case infoNoise.MatchString(l), adviceRE.MatchString(l):
			dropped++
			continue
		}
		if m := logPathRE.FindStringSubmatch(l); m != nil && seenLog[m[1]] {
			dropped++
			continue
		}
		if i := strings.Index(l, "(see "); i >= 0 && strings.HasPrefix(l, "FAIL: ") {
			seenLog[strings.TrimSuffix(l[i+5:], ")")] = true
		}
		if m := cmdTailRE.FindStringSubmatch(l); m != nil {
			chars += len(l) - len(m[1])
			l = m[1]
		}
		kept = append(kept, l)
	}
	return kept, dropped, chars
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
