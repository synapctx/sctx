// Package helm formats `helm` output. Release commands (install, upgrade,
// status, rollback) end in a NOTES block that restates how to reach the app;
// `--dry-run`/`template` manifests repeat the same chart-label boilerplate in
// every resource; `repo update` and `dependency update` print one line per
// repository. The aggressive tier elides those with explicit …+N markers and
// keeps release metadata, resource tables, manifests, warnings and every error.
// Subcommands with nothing safely droppable (list, history, show, get) decline.
package helm

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders helm command output.
type Formatter struct{}

// New returns the helm formatter.
func New() format.Formatter { return &Formatter{} }

// Descriptor claims every helm invocation.
func (*Formatter) Descriptor() format.Match { return format.Match{Command: "helm"} }

var (
	ansiRE    = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	sectionRE = regexp.MustCompile(`^[A-Z][A-Z -]+:$`)
	repoOKRE  = regexp.MustCompile(`^\.\.\.Successfully got an update from the "([^"]+)" chart repository$`)
	iconRE    = regexp.MustCompile(`^\[INFO\] Chart\.yaml: icon is recommended$`)
	boilerRE  = regexp.MustCompile(`^\s+(helm\.sh/chart: \S+|app\.kubernetes\.io/managed-by: Helm)$`)
)

// subcommand returns argv's helm subcommand, folding "repo update" and
// "dependency update" into one word.
func subcommand(argv []string) string {
	var words []string
	skipNext := false
	for _, a := range argv[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			// A space-separated flag value must not be mistaken for the
			// subcommand.
			switch a {
			case "-n", "--namespace", "--kube-context", "--kubeconfig", "--kube-as-user", "--kube-token", "--registry-config", "--repository-config", "--repository-cache":
				skipNext = true
			}
			continue
		}
		words = append(words, a)
		if len(words) == 2 {
			break
		}
	}
	if len(words) == 0 {
		return ""
	}
	switch words[0] {
	case "repo", "dependency", "dep", "get", "show", "plugin", "registry":
		if len(words) == 2 {
			if words[0] == "dep" {
				words[0] = "dependency"
			}
			return words[0] + " " + words[1]
		}
	}
	return words[0]
}

func hasFlag(argv []string, names ...string) bool {
	for _, a := range argv[1:] {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

func read(r io.Reader) (string, error) {
	if r == nil {
		return "", nil
	}
	b, err := io.ReadAll(r)
	return string(b), err
}

// Aggressive elides NOTES/COMPUTED VALUES, manifest label boilerplate, repeated
// repo lines and the lint icon nag.
func (*Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed only collapses the repo/dependency progress lines.
func (*Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 || hasFlag(in.Argv, "-o", "--output", "--help", "-h") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	sub := subcommand(in.Argv)
	switch sub {
	case "install", "upgrade", "status", "rollback", "template", "lint",
		"repo update", "dependency update", "dependency build":
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, err := read(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("helm: reading stdout: %w", err)
	}
	stderr, err := read(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("helm: reading stderr: %w", err)
	}
	if stdout == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := strings.Split(strings.TrimSuffix(ansiRE.ReplaceAllString(stdout, ""), "\n"), "\n")

	var out, repos []string
	counts := map[string]int{}
	var parts []string
	skipping := ""
	skipped := 0
	flush := func() {
		if skipping != "" {
			out = append(out, fmt.Sprintf("%s …+%d lines", skipping, skipped))
			counts[strings.ToLower(strings.TrimSuffix(skipping, ":"))] += skipped
		}
		skipping, skipped = "", 0
	}
	manifest := sub == "template"
	repoAt := -1
	var repoRaw []string
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if sectionRE.MatchString(l) {
			flush()
			manifest = l == "MANIFEST:" || l == "HOOKS:"
			if aggressive && (l == "NOTES:" || l == "COMPUTED VALUES:") && sub != "lint" {
				skipping = l
				continue
			}
		}
		if skipping != "" {
			if strings.TrimSpace(l) != "" {
				skipped++
			}
			continue
		}
		if m := repoOKRE.FindStringSubmatch(l); m != nil {
			if repoAt < 0 {
				repoAt = len(out)
			}
			repos = append(repos, m[1])
			repoRaw = append(repoRaw, l)
			continue
		}
		if strings.HasPrefix(l, "Hang tight while we grab") {
			counts["progress"]++
			continue
		}
		if aggressive && iconRE.MatchString(l) {
			counts["icon nag"]++
			continue
		}
		if aggressive && manifest && boilerRE.MatchString(l) {
			counts["chart labels"]++
			continue
		}
		if aggressive && strings.TrimSpace(l) == "" && (sub == "template" || manifest) {
			counts["blank"]++
			continue
		}
		out = append(out, l)
	}
	flush()
	if repoAt >= 0 {
		repl := repoRaw
		if len(repos) > 1 {
			repl = []string{fmt.Sprintf("...Successfully got an update from %d chart repositories: %s", len(repos), strings.Join(repos, ", "))}
			counts["repo lines"] += len(repos) - 1
		}
		out = append(out[:repoAt], append(repl, out[repoAt:]...)...)
	}
	n := 0
	for _, k := range []string{"notes", "computed values", "chart labels", "icon nag", "progress", "repo lines", "blank"} {
		if counts[k] > 0 {
			n += counts[k]
			parts = append(parts, fmt.Sprintf("%s ×%d", k, counts[k]))
		}
	}
	if n == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	fmt.Fprintf(&b, "…+%d lines (%s)\n", n, strings.Join(parts, ", "))
	b.WriteString(strings.Join(out, "\n"))
	b.WriteByte('\n')
	fold := false
	if stderr != "" {
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteByte('\n')
		}
		fold = true
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: fold, Elided: true}, nil
}
