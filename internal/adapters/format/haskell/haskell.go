// Package haskell formats `cabal build/test` and `ghc` output. Cabal's plan,
// configure/preprocess/download/install chatter and GHC's "[n of m] Compiling"
// rows are dropped; every GHC warning and error keeps its location line, its
// [GHC-code] [-Wflag] tag and its message but loses the echoed source line and
// caret; hspec's passing/failing tree (restated by its Failures section) is
// dropped while the failures, "To rerun" hints and totals are kept.
package haskell

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders one program's output (cabal or ghc). It holds no state.
type Formatter struct{ command string }

// All returns the cabal and ghc formatters; the registry holds one entry per program.
func All() []format.Formatter {
	return []format.Formatter{&Formatter{command: "cabal"}, &Formatter{command: "ghc"}}
}

// Descriptor claims every invocation of the program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

var (
	ansiRE   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	headerRE = regexp.MustCompile(`^\S.*?:(?:\d+:\d+(?:-\d+)?|\(\d+,\d+\)-\(\d+,\d+\)): (?:warning|error)\b`)
	gutterRE = regexp.MustCompile(`^\s*\d*\s*\|(?: .*)?$`)
	noiseRE  = regexp.MustCompile(`^(?:Resolving dependencies\.\.\.|Build profile:|In order, the following (?:will|would) be built|` +
		` - \S+-[\d.]+ \(|(?:Configuring|Preprocessing|Building) (?:library|executable|test suite|benchmark|foreign library)|` +
		`(?:Downloading|Downloaded|Starting|Building|Installing|Completed)\s+\S+|\[\d+ of \d+\] (?:Compiling|Linking)|` +
		`Running \d+ test suites\.\.\.|Test suite \S+: RUNNING\.\.\.|Finished in [\d.]+ seconds|Randomized with seed \d+|Loaded package environment)`)
	logPathRE = regexp.MustCompile(`^Test suite logged to:$`)
	hspecItem = regexp.MustCompile(`^\s+.* \[(?:✔|✘|‐)\]$`)
)

func applicable(command string, argv []string) bool {
	if command == "ghc" {
		for _, a := range argv[1:] {
			switch {
			case a == "-e", a == "--interactive", a == "--version", a == "-V", a == "--info", a == "--help", a == "-M", a == "-E",
				strings.HasPrefix(a, "--print-"), strings.HasPrefix(a, "--show-"), strings.HasPrefix(a, "-ddump"), a == "--numeric-version", a == "--supported-extensions":
				return false
			}
		}
		return len(argv) > 1
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "--help") || a == "-v3" || a == "-v2" || a == "-h" {
			return false
		}
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "build", "test", "v2-build", "v2-test":
			return true
		}
		return false
	}
	return false
}

// Aggressive drops cabal chatter, source echoes and the hspec tree.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, true)
}

// Relaxed drops cabal chatter and source echoes only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !applicable(f.command, in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("haskell: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("haskell: reading stderr: %w", err)
	}
	// GHC diagnostics are on stderr and precede cabal's stdout trailer.
	text := ansiRE.ReplaceAllString(string(rawOut)+string(rawErr), "")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept, dropped := filter(strings.Split(text, "\n"), aggressive)
	if dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d lines (build chatter, source echoes, blanks, passing-test tree)", dropped))
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func filter(lines []string, aggressive bool) (kept []string, dropped int) {
	inGutter := false
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t\r")
		switch {
		case l == "":
			inGutter = false
			dropped++
			continue
		case headerRE.MatchString(l):
			inGutter = false
			kept = append(kept, l)
			continue
		case inGutter && gutterRE.MatchString(l):
			dropped++
			continue
		case gutterRE.MatchString(l) && strings.TrimSpace(l) == "|":
			// A bare gutter opens the excerpt block of the diagnostic above.
			inGutter = true
			dropped++
			continue
		case noiseRE.MatchString(l):
			dropped++
			continue
		case logPathRE.MatchString(l):
			dropped++
			if i+1 < len(lines) {
				i++
				dropped++
			}
			continue
		}
		if aggressive {
			if hspecItem.MatchString(l) {
				dropped++
				continue
			}
			// An hspec group heading: an unindented line directly above an item.
			if !strings.HasPrefix(l, " ") && i+1 < len(lines) && hspecItem.MatchString(strings.TrimRight(lines[i+1], " \t\r")) {
				dropped++
				continue
			}
		}
		kept = append(kept, l)
	}
	return kept, dropped
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
