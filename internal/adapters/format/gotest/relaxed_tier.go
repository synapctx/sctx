package gotest

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// relaxedRender applies generic line-level filtering shared by every `go`
// subcommand: it collapses runs of identical lines, drops module-fetch
// progress noise, and otherwise keeps everything (error signal is
// preserved by never dropping a line we don't recognize as noise).
func relaxedRender(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	outLines := relaxedFilter(string(stdout))
	errLines := relaxedFilter(string(stderr))

	var b strings.Builder
	for _, l := range outLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	foldStderr := false
	if len(errLines) > 0 {
		for _, l := range errLines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		foldStderr = true
	}

	body := b.String()
	if body == "" {
		body = fmt.Sprintf("(no diagnostic lines; %d bytes filtered)\n", len(stdout)+len(stderr))
	}

	return format.Rendered{Body: []byte(body), FoldStderr: foldStderr}, nil
}

// relaxedFilter collapses consecutive identical lines to "line ×N" and
// drops `go` module-fetch progress noise (downloading/extracting/finding),
// summarizing dropped download lines into a single count.
func relaxedFilter(text string) []string {
	lines := splitLines(text)
	if lines == nil {
		return nil
	}

	out := make([]string, 0, len(lines))
	downloadCount := 0

	i := 0
	for i < len(lines) {
		l := lines[i]

		switch {
		case strings.HasPrefix(l, "go: downloading"):
			downloadCount++
			i++
			continue
		case strings.HasPrefix(l, "go: extracting"), strings.HasPrefix(l, "go: finding"):
			i++
			continue
		}

		j := i + 1
		for j < len(lines) && lines[j] == l {
			j++
		}
		n := j - i
		if n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", l, n))
		} else if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
		i = j
	}

	if downloadCount > 0 {
		out = append([]string{fmt.Sprintf("downloaded %d modules", downloadCount)}, out...)
	}

	return out
}
