// Package taskrunner formats `just`, `task` and `mise run`. These runners
// print one banner line per command to STDERR and leave the command's own
// output on the command's streams, so the two cannot be re-interleaved. What
// can be done safely is the single-command case: when the banners name exactly
// one command and the registry has a formatter for it, the command's output
// (stdout plus the stderr that is not runner banner or footer) is handed to
// that formatter and the runner's own lines are kept around it. Anything else
// declines and the output stays as the runner printed it.
package taskrunner

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/nested"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/nestedcmd"
)

// Resolver looks up the formatter for the inner command; injected by main.
type Resolver = nested.Resolver

type runner struct {
	// banner returns the command a banner line announces.
	banner func(line string) (cmd string, ok bool)
	// footer reports the runner's own failure summary lines.
	footer func(line string) bool
	// declined reports invocation flags that change the output contract.
	declined func(argv []string) bool
	// firstLineOnly means banners are indistinguishable from command output
	// (just echoes the bare recipe line), so only the FIRST stderr line may be
	// one, and only for a program in allow.
	firstLineOnly bool
}

var (
	taskBannerRE = regexp.MustCompile(`^task: \[[^\]]+\] (.+)$`)
	taskFooterRE = regexp.MustCompile(`^task: Failed to run task ".*": exit status \d+$`)
	miseBannerRE = regexp.MustCompile(`^\[[^\]]+\] \$ (.+)$`)
	miseFooterRE = regexp.MustCompile(`^\[[^\]]+\] ERROR task failed$`)
	justFooterRE = regexp.MustCompile("^error: [Rr]ecipe `.*` (failed on line \\d+ with exit code \\d+|was terminated on line \\d+ by signal \\d+)$")
)

// justAllow lists the programs a bare just echo line may name. just prints the
// recipe line with no marker, so a line of command OUTPUT could be mistaken for
// one; a short list of build/test tools whose output never begins that way keeps
// that from happening.
var justAllow = map[string]bool{
	"go": true, "golangci-lint": true, "cargo": true, "npm": true, "npx": true, "pytest": true,
	"ruff": true, "mypy": true, "tsc": true, "eslint": true, "pip": true, "terraform": true,
	"make": true, "cmake": true, "ctest": true, "ninja": true, "swift": true, "clang": true,
	"gcc": true, "cc": true, "pnpm": true, "yarn": true, "docker": true, "kubectl": true,
}

func justBanner(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !justAllow[fields[0]] || strings.HasPrefix(line, " ") {
		return "", false
	}
	return line, true
}

// Formatter renders one task runner.
type Formatter struct {
	match   format.Match
	runner  runner
	resolve Resolver
}

// All returns the just, task, `mise run` and `mise r` formatters.
func All(resolve Resolver) []format.Formatter {
	just := runner{
		banner: justBanner, footer: justFooterRE.MatchString, firstLineOnly: true,
		declined: declinedByFlag("-l", "--list", "--summary", "-s", "--show", "--evaluate", "--dump", "--choose", "--fmt", "--init",
			"--completions", "--variables", "--groups", "--man", "--help", "-h", "--version", "-V", "--dry-run", "-n", "--command", "-c", "--edit", "-e"),
	}
	task := runner{
		banner: func(l string) (string, bool) {
			m := taskBannerRE.FindStringSubmatch(l)
			if m == nil {
				return "", false
			}
			return m[1], true
		},
		footer: taskFooterRE.MatchString,
		declined: declinedByFlag("-l", "--list", "-a", "--list-all", "--summary", "-n", "--dry", "-w", "--watch", "--help", "-h", "--version",
			"--init", "-i", "--json", "--status", "--silent", "-s", "--interactive"),
	}
	mise := runner{
		banner: func(l string) (string, bool) {
			m := miseBannerRE.FindStringSubmatch(l)
			if m == nil {
				return "", false
			}
			return m[1], true
		},
		footer:   miseFooterRE.MatchString,
		declined: declinedByFlag("--help", "-h", "--dry-run", "-n", "--watch", "-w", "--no-header", "--quiet", "-q", "--output", "-o", "--timings", "--jobs", "-j", "--continue-on-error"),
	}
	return []format.Formatter{
		&Formatter{match: format.Match{Command: "just"}, runner: just, resolve: resolve},
		&Formatter{match: format.Match{Command: "task"}, runner: task, resolve: resolve},
		&Formatter{match: format.Match{Command: "mise", Subcommands: []string{"run"}}, runner: mise, resolve: resolve},
		&Formatter{match: format.Match{Command: "mise", Subcommands: []string{"r"}}, runner: mise, resolve: resolve},
	}
}

func declinedByFlag(flags ...string) func([]string) bool {
	set := map[string]bool{}
	for _, f := range flags {
		set[f] = true
	}
	return func(argv []string) bool {
		for _, a := range argv[1:] {
			if a == "--" {
				return false
			}
			if before, _, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
				a = before
			}
			if set[a] {
				return true
			}
		}
		return false
	}
}

// Descriptor returns this runner's claim.
func (f *Formatter) Descriptor() format.Match { return f.match }

// Aggressive delegates to the inner formatter's Aggressive tier.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.delegate(ctx, in, true)
}

// Relaxed delegates to the inner formatter's Relaxed tier.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.delegate(ctx, in, false)
}

func (f *Formatter) delegate(ctx context.Context, in format.Input, aggressive bool) (format.Rendered, error) {
	if f.resolve == nil || f.runner.declined(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, err
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, err
	}

	var banners, footers, rest []string
	var cmd string
	for i, l := range splitLines(stderr) {
		switch {
		case f.runner.footer(l):
			footers = append(footers, l)
			continue
		case !f.runner.firstLineOnly || i == 0:
			if c, ok := f.runner.banner(l); ok {
				banners = append(banners, l)
				if cmd == "" {
					cmd = c
				}
				continue
			}
		}
		rest = append(rest, l)
	}
	if len(banners) != 1 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	argv, ok := nestedcmd.Remote([]string{cmd})
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	innerErr := []byte(strings.Join(rest, "\n"))
	if len(rest) > 0 {
		innerErr = append(innerErr, '\n')
	}
	innerIn := format.Input{
		Argv: in.Argv, Stdout: bytes.NewReader(stdout), Stderr: bytes.NewReader(innerErr),
		ExitCode: in.ExitCode, Duration: in.Duration,
	}
	fm, inner, err := nested.Prepare(f.resolve, innerIn, argv, nil)
	if err != nil {
		return format.Rendered{}, err
	}
	var out format.Rendered
	if aggressive {
		out, err = fm.Aggressive(ctx, inner)
	} else {
		out, err = fm.Relaxed(ctx, inner)
	}
	if err != nil {
		return format.Rendered{}, err
	}
	body := append([]string{}, banners...)
	body = append(body, strings.TrimRight(string(out.Body), "\n"))
	if !out.FoldStderr && len(rest) > 0 {
		body = append(body, rest...)
	}
	body = append(body, footers...)
	joined := strings.Join(body, "\n")
	if len(joined) >= len(stdout)+len(stderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(joined), FoldStderr: true, Elided: out.Elided, Note: out.Note}, nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}
