// Package nix formats `nix build`, `nix flake check`, `nix-build` and
// `nix-shell --run`. The store-path bullet lists under "will be built/fetched",
// every "copying path ... from cache" row and flake-check progress
// ("checking derivation ...", "derivation evaluated to ...") are dropped and
// counted; summaries ("these 25 paths will be fetched (...)"), builder output,
// "building ... .drv" rows, evaluation errors (with their excerpts) and the
// build verdict are kept.
package nix

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders one program's output. It holds no mutable state.
type Formatter struct{ command string }

// All returns one formatter per supported program.
func All() []format.Formatter {
	names := []string{"nix", "nix-build", "nix-shell"}
	out := make([]format.Formatter, len(names))
	for i, n := range names {
		out[i] = &Formatter{command: n}
	}
	return out
}

// Descriptor claims every invocation of the program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	storeItem  = regexp.MustCompile(`^  /nix/store/\S+$`)
	copyingRE  = regexp.MustCompile(`^copying path '/nix/store/\S+' from '\S+'\.\.\.$`)
	progressRE = regexp.MustCompile(`^(?:checking (?:derivation|flake output) |derivation evaluated to /nix/store/|evaluating flake\.\.\.$|unpacking '.*' into the Git cache|` +
		`fetching (?:tarball|git|path)? ?\S* ?$|evaluating derivation '|querying info about )`)
)

func applicable(command string, argv []string) bool {
	for _, a := range argv[1:] {
		if a == "--json" || strings.HasPrefix(a, "--log-format") || a == "--help" || a == "-h" || a == "--version" {
			return false
		}
	}
	switch command {
	case "nix-build":
		return true
	case "nix-shell":
		// Without --run/--command nix-shell opens an interactive shell.
		for _, a := range argv[1:] {
			if a == "--run" || a == "--command" {
				return true
			}
		}
		return false
	}
	var words []string
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		words = append(words, a)
		if len(words) == 2 {
			break
		}
	}
	switch {
	case len(words) >= 1 && words[0] == "build":
		return true
	case len(words) == 2 && words[0] == "flake" && words[1] == "check":
		return true
	}
	return false
}

// Aggressive drops path lists, copy rows and flake-check progress.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, true)
}

// Relaxed drops only the "copying path" rows and the store-path lists.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	if !applicable(f.command, in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("nix: reading stdout: %w", err)
	}
	rawErr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("nix: reading stderr: %w", err)
	}
	// nix logs to stderr and prints result paths on stdout: stderr first keeps
	// the log in order and the payload path last.
	text := ansiRE.ReplaceAllString(string(rawErr)+string(rawOut), "")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if text = strings.TrimRight(text, "\n"); text == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var kept []string
	var items, copies, progress int
	for _, raw := range strings.Split(text, "\n") {
		l := strings.TrimRight(raw, " \t")
		switch {
		case storeItem.MatchString(l):
			items++
		case copyingRE.MatchString(l):
			copies++
		case aggressive && progressRE.MatchString(l):
			progress++
		default:
			kept = append(kept, l)
		}
	}
	if items+copies+progress == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if items > 0 {
		kept = append(kept, fmt.Sprintf("…+%d store paths listed", items))
	}
	if copies > 0 {
		kept = append(kept, fmt.Sprintf("…+%d copying-path rows", copies))
	}
	if progress > 0 {
		kept = append(kept, fmt.Sprintf("…+%d evaluation/check progress rows", progress))
	}
	body := strings.Join(kept, "\n") + "\n"
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
