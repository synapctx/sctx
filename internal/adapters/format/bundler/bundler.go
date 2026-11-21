// Package bundler formats `bundle install|update|add|remove`: the per-gem
// Fetching/Installing/Using chatter collapses into one list of installed gems
// plus the "Bundle complete!" summary, and every other line (errors, resolver
// conflicts, post-install messages) is kept. `bundle exec <tool>` delegates to
// the formatter of the tool it runs.
package bundler

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/nested"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Resolver looks up the formatter for the tool run under `bundle exec`. It is
// injected by main to keep this adapter independent of the registry.
type Resolver = nested.Resolver

// Formatter renders `bundle` output.
type Formatter struct{ resolve Resolver }

// New constructs a bundler Formatter. The optional resolver enables
// `bundle exec` delegation.
func New(resolve ...Resolver) *Formatter {
	f := &Formatter{}
	if len(resolve) > 0 {
		f.resolve = resolve[0]
	}
	return f
}

// Descriptor claims every `bundle` invocation; dispatch happens inside.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "bundle"} }

var (
	opRE       = regexp.MustCompile(`^(Fetching|Installing) (\S+) (\S+)(?: (.*))?$`)
	usingRE    = regexp.MustCompile(`^Using \S+ \S+`)
	completeRE = regexp.MustCompile(`^Bundle (complete|updated)!`)
	noiseRE    = regexp.MustCompile(`^(?:Fetching gem metadata from |Resolving dependencies|Bundled gems are installed into |Use ` + "`bundle info" + `|\d+ (?:installed )?gems? you (?:directly )?depend on (?:is|are) looking for funding|Run ` + "`bundle fund`" + `|Fetching source index from )`)
	ansiRE     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

// subcommand returns the first non-flag argument and its index in argv.
func subcommand(argv []string) (string, int) {
	for i := 1; i < len(argv); i++ {
		if !strings.HasPrefix(argv[i], "-") {
			return argv[i], i
		}
	}
	return "", 0
}

func execArgv(argv []string, at int) []string {
	rest := argv[at+1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	return rest
}

// Aggressive collapses an install/update run.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, at := subcommand(in.Argv)
	switch sub {
	case "exec":
		return nested.Aggressive(ctx, f.resolve, in, execArgv(in.Argv, at), nil)
	case "install", "update", "add", "remove":
		for _, a := range in.Argv {
			if a == "--verbose" || a == "-V" {
				// Verbose mode interleaves HTTP and resolver traces this
				// package does not parse.
				return format.Rendered{}, format.ErrTierInapplicable
			}
		}
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readStreams(in)
	if err != nil {
		return format.Rendered{}, err
	}
	return render(stdout, stderr)
}

// Relaxed delegates `bundle exec`; for everything else it only collapses
// repeated lines, since the output shape is not one this package parses.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	sub, at := subcommand(in.Argv)
	if sub == "exec" {
		return nested.Relaxed(ctx, f.resolve, in, execArgv(in.Argv, at), nil)
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
			if l == "" {
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

// render collapses Fetching/Installing/Using lines. Every other line is kept,
// so a failure's diagnostics are never lost; a Fetching line with no matching
// Installing line (a download that did not finish installing) is reported.
func render(stdout, stderr string) (format.Rendered, error) {
	lines := splitLines(ansiRE.ReplaceAllString(stdout, ""))
	if len(splitLines(stderr)) > 0 {
		lines = append(lines, splitLines(ansiRE.ReplaceAllString(stderr, ""))...)
	}
	type gem struct{ name, version, rest string }
	var installed []gem
	fetched := map[string]gem{}
	var fetchOrder []string
	var kept []string
	using, noise := 0, 0
	recognized := false
	for _, raw := range lines {
		l := strings.TrimRight(raw, " \t")
		if l == "" {
			continue
		}
		if noiseRE.MatchString(l) {
			noise++
			continue
		}
		if m := opRE.FindStringSubmatch(l); m != nil {
			recognized = true
			g := gem{m[2], m[3], m[4]}
			if m[1] == "Fetching" {
				if _, dup := fetched[g.name]; !dup {
					fetchOrder = append(fetchOrder, g.name)
				}
				fetched[g.name] = g
			} else {
				installed = append(installed, g)
				delete(fetched, g.name)
			}
			continue
		}
		switch {
		case usingRE.MatchString(l):
			recognized = true
			using++
		default:
			if completeRE.MatchString(l) {
				recognized = true
			}
			kept = append(kept, l)
		}
	}
	if !recognized {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	if len(installed) > 0 {
		parts := make([]string, len(installed))
		for i, g := range installed {
			parts[i] = g.name + " " + g.version
			if g.rest != "" {
				parts[i] += " " + g.rest
			}
		}
		fmt.Fprintf(&b, "installed %d: %s\n", len(installed), strings.Join(parts, ", "))
	}
	var stuck []string
	for _, n := range fetchOrder {
		if g, ok := fetched[n]; ok {
			stuck = append(stuck, g.name+" "+g.version)
		}
	}
	if len(stuck) > 0 {
		fmt.Fprintf(&b, "fetched but not installed %d: %s\n", len(stuck), strings.Join(stuck, ", "))
	}
	if using > 0 {
		fmt.Fprintf(&b, "using ×%d gems already installed\n", using)
	}
	for _, l := range kept {
		b.WriteString(l + "\n")
	}
	if noise > 0 {
		fmt.Fprintf(&b, "…+%d lines (metadata, hints)\n", noise)
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != "", Elided: true}, nil
}

func readStreams(in format.Input) (stdout, stderr string, err error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return "", "", fmt.Errorf("bundler: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return "", "", fmt.Errorf("bundler: reading stderr: %w", err)
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
