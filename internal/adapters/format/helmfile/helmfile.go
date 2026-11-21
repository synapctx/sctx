// Package helmfile formats `helmfile` output. Two things dominate it: stderr
// progress chatter ("Building dependency release=...", "Templating
// release=...") and, on failure, a per-release diagnostic block whose ARGS
// section prints one `N: value (M bytes)` line per argument of the helm
// command that failed. The aggressive tier drops the progress lines with a
// count, joins each ARGS block onto one line, and hands `helmfile template`'s
// manifests to the helm formatter. Every error line is kept.
package helmfile

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/helm"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders helmfile output.
type Formatter struct{ helm format.Formatter }

// New returns the helmfile formatter.
func New() *Formatter { return &Formatter{helm: helm.New()} }

// Descriptor claims every helmfile invocation.
func (*Formatter) Descriptor() format.Match { return format.Match{Command: "helmfile"} }

var (
	progressRE = regexp.MustCompile(`^(Building dependency|Templating|Linting) release=`)
	argLineRE  = regexp.MustCompile(`^\s+\d+: (.*) \(\d+ bytes\)$`)
	valueFlags = map[string]bool{
		"-f": true, "--file": true, "-e": true, "--environment": true,
		"-l": true, "--selector": true, "-n": true, "--namespace": true,
		"--state-values-file": true, "--state-values-set": true,
		"--kube-context": true, "--kubeconfig": true, "--helm-binary": true,
		"--chart": true, "--log-level": true, "-b": true,
	}
)

// subcommand returns the first non-flag argument after argv[0], skipping the
// values of known value-taking global flags; "" when it cannot tell.
func subcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "-") {
			return a
		}
		if valueFlags[a] {
			i++
		}
	}
	return ""
}

func machineOutput(argv []string) bool {
	for _, a := range argv[1:] {
		if a == "--" {
			return false
		}
		if a == "-o" || a == "--output" || strings.HasPrefix(a, "--output=") || a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// Aggressive drops progress lines, compacts ARGS blocks, and for a successful
// `template` renders the manifests with the helm formatter.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.render(ctx, in, true)
}

// Relaxed only drops progress lines and compacts ARGS blocks.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return f.render(ctx, in, false)
}

func (f *Formatter) render(ctx context.Context, in format.Input, delegate bool) (format.Rendered, error) {
	if len(in.Argv) < 2 || machineOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	sub := subcommand(in.Argv)
	if sub == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("helmfile: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("helmfile: reading stderr: %w", err)
	}

	errOut, progress, joined := compactStderr(stderr)
	body := stdout
	helmRendered := false
	if delegate && sub == "template" && in.ExitCode == 0 && stdout != "" {
		r, herr := f.helm.Aggressive(ctx, format.Input{
			Argv: []string{"helm", "template"}, Command: "helm template",
			Stdout: strings.NewReader(stdout),
		})
		if herr == nil {
			body, helmRendered = string(r.Body), true
		}
	}
	if progress == 0 && joined == 0 && !helmRendered {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	if progress > 0 {
		fmt.Fprintf(&b, "…+%d helmfile progress lines (Building dependency/Templating/Linting)\n", progress)
	}
	b.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(errOut)
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != "", Elided: progress > 0 || helmRendered}, nil
}

// compactStderr drops progress lines (counted) and joins each ARGS block of
// `N: value (M bytes)` lines into one `ARGS: a b c` line (counted in joined).
func compactStderr(stderr string) (out string, progress, joined int) {
	if stderr == "" {
		return "", 0, 0
	}
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	var kept []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if progressRE.MatchString(l) {
			progress++
			continue
		}
		if strings.TrimSpace(l) == "ARGS:" {
			var args []string
			j := i + 1
			for j < len(lines) {
				m := argLineRE.FindStringSubmatch(lines[j])
				if m == nil {
					break
				}
				args = append(args, m[1])
				j++
			}
			if len(args) > 0 {
				kept = append(kept, "ARGS: "+strings.Join(args, " "))
				joined++
				i = j - 1
				continue
			}
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return "", progress, joined
	}
	return strings.Join(kept, "\n") + "\n", progress, joined
}

func readAll(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	b, err := io.ReadAll(r)
	return string(b), err
}
