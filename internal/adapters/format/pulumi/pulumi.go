// Package pulumi formats `pulumi preview|up|destroy|refresh` output. Non-TTY
// pulumi prints each resource twice (an in-progress line and a completion
// line), `up`/`destroy` repeat the whole preview before the update, and a
// first run prints plugin download chatter. The aggressive tier drops that
// redundancy and keeps every resource outcome, output change, diagnostic and
// the summary; anything unrecognised declines to the caller verbatim.
package pulumi

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders pulumi command output.
type Formatter struct{}

// New returns the pulumi formatter.
func New() format.Formatter { return &Formatter{} }

// Descriptor claims every pulumi invocation; non-update subcommands decline.
func (*Formatter) Descriptor() format.Match { return format.Match{Command: "pulumi"} }

var (
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// pluginRE is plugin download/install chatter and the "@ updating...." ticker.
	pluginRE = regexp.MustCompile(`^(Downloading|Installing) plugin |^@ (previewing|updating|destroying|refreshing|watching)\b`)
	// progressRE is a resource's in-progress line; its completion line follows.
	progressRE = regexp.MustCompile(`^\s*\S{1,3}\s+\S+ \S+ (creating|updating|deleting|replacing|reading|refreshing|importing|creating replacement|deleting original|discarding) \(\d+(\.\d+)?s\)`)
	phaseRE    = regexp.MustCompile(`^(Previewing|Updating|Destroying|Refreshing|Watching)\b.*:$`)
	updatePhRE = regexp.MustCompile(`^(Updating|Destroying|Refreshing)\b.*:$`)
)

// updateSub reports whether argv's subcommand is one this package renders.
func updateSub(argv []string) bool {
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		switch a {
		case "preview", "up", "update", "destroy", "refresh", "down":
			return true
		}
		return false
	}
	return false
}

func machine(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "--json", "-j", "--help", "-h", "--diff", "--show-config", "--show-replacement-steps", "--debug", "--verbose", "-v":
			return true
		}
		if strings.HasPrefix(a, "--json=") || strings.HasPrefix(a, "--verbose=") {
			return true
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

// Aggressive drops the duplicated preview, progress lines and plugin chatter.
func (*Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, true)
}

// Relaxed drops only plugin chatter and in-progress resource lines.
func (*Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in, false)
}

func render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 || !updateSub(in.Argv) || machine(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, err := read(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("pulumi: reading stdout: %w", err)
	}
	stderr, err := read(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("pulumi: reading stderr: %w", err)
	}
	if stdout == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	plain := make([]string, len(lines))
	hasUpdate := false
	for i, l := range lines {
		plain[i] = ansiRE.ReplaceAllString(strings.TrimRight(l, "\r"), "")
		if updatePhRE.MatchString(plain[i]) {
			hasUpdate = true
		}
	}

	var out []string
	counts := map[string]int{}
	recognized := false
	inPreview := false
	for i, l := range lines {
		p := plain[i]
		switch {
		case pluginRE.MatchString(p):
			counts["plugin"]++
			continue
		case phaseRE.MatchString(p):
			recognized = true
			// The preview before an update is fully restated by the update.
			inPreview = aggressive && hasUpdate && strings.HasPrefix(p, "Previewing")
			if updatePhRE.MatchString(p) {
				inPreview = false
			}
			if inPreview {
				counts["preview"]++
				continue
			}
		case inPreview:
			counts["preview"]++
			continue
		case progressRE.MatchString(p):
			counts["progress"]++
			continue
		}
		if aggressive && strings.TrimSpace(p) == "" {
			if n := len(out); n == 0 || out[n-1] == "" {
				counts["blank"]++
				continue
			}
		}
		if aggressive {
			out = append(out, p)
		} else {
			out = append(out, l)
		}
	}
	n := 0
	var parts []string
	for _, k := range []string{"plugin", "preview", "progress", "blank"} {
		if counts[k] > 0 {
			n += counts[k]
			parts = append(parts, fmt.Sprintf("%s ×%d", k, counts[k]))
		}
	}
	if !recognized || n == 0 {
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
