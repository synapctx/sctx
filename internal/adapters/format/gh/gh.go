// Package gh implements native-output formatters for the GitHub CLI.
package gh

import (
	"bufio"
	"bytes"
	"context"
	"io"

	"github.com/synapctx/sctx/internal/adapters/format/generic"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/platform/ghargv"
)

type Formatter struct{}

func New() *Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "gh"} }

// Dedicated keeps generic read-only reachability distinct from a purpose-built
// gh renderer in telemetry.
func (f *Formatter) Dedicated(argv []string) bool {
	inv, ok := ghargv.Parse(argv)
	if !ok {
		return false
	}
	switch inv.Level1 + " " + inv.Level2 {
	case "pr list", "pr view", "pr checks", "pr status", "pr diff",
		"issue list", "issue view", "issue status", "run list", "run view",
		"repo list", "repo view", "release list", "release view", "api ":
		return true
	default:
		return false
	}
}

func subcommand(argv []string) (level1, level2 string, rest []string) {
	inv, ok := ghargv.Parse(argv)
	if !ok {
		return "", "", nil
	}
	return inv.Level1, inv.Level2, inv.Args
}

func customOutput(argv []string) bool {
	return ghargv.HasOption(argv, "--jq", "-q", "--template", "-t")
}

func jsonOutput(argv []string) bool { return ghargv.HasOption(argv, "--json") }

func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if customOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	l1, l2, args := subcommand(in.Argv)
	if l1 == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	// Compact only JSON the user or native API command actually requested.
	if l1 == "api" || jsonOutput(in.Argv) {
		if in.ExitCode != 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return generic.New().Aggressive(ctx, in)
	}

	// Failed checks use exit 1 as a query result rather than a transport error.
	if l1 == "pr" && l2 == "checks" {
		if len(readAll(in.Stderr)) > 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return aggressiveChecks(in)
	}
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	switch {
	case l1 == "pr" && l2 == "list":
		return aggressiveList(in, listPullRequests)
	case l1 == "issue" && l2 == "list":
		return aggressiveList(in, listIssues)
	case l1 == "repo" && l2 == "list":
		return aggressiveList(in, listRepositories)
	case l1 == "release" && l2 == "list":
		return aggressiveList(in, listReleases)
	case (l1 == "pr" || l1 == "issue") && l2 == "view":
		return aggressiveView(in, viewOptions{bodyCap: 40})
	case l1 == "repo" && l2 == "view":
		return aggressiveView(in, viewOptions{bodyCap: 40})
	case l1 == "release" && l2 == "view":
		return aggressiveView(in, viewOptions{bodyCap: 50, repeatedKey: "asset", repeatedCap: 12})
	case (l1 == "pr" || l1 == "issue") && l2 == "status":
		return aggressiveStatus(in)
	case l1 == "run" && l2 == "list":
		return aggressiveRunList(in)
	case l1 == "run" && l2 == "view":
		return aggressiveRunView(in, args)
	case l1 == "pr" && l2 == "diff":
		return aggressivePRDiff(ctx, in, args)
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
}

func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 || customOutput(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)
	if bytes.IndexByte(rawStdout, 0) >= 0 || bytes.IndexByte(rawStderr, 0) >= 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	attempt := in
	attempt.Stdout = bytes.NewReader(rawStdout)
	attempt.Stderr = bytes.NewReader(rawStderr)
	return generic.New().Relaxed(ctx, attempt)
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
