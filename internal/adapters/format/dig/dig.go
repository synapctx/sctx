// Package dig filters stable protocol metadata from native full `dig` output
// while preserving the DNS response header, flags, question, records, timing,
// server, warnings, and any line it does not explicitly recognize.
package dig

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type Formatter struct{}

func New() *Formatter { return &Formatter{} }

func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "dig"} }

func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("dig: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("dig: reading stderr: %w", err)
	}
	if len(stderr) > 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	lines := splitLines(string(raw))
	kept := make([]string, 0, len(lines))
	dropped := 0
	sawHeader, sawQuestion := false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "; <<>> DiG "),
			strings.HasPrefix(line, ";; global options:"),
			line == ";; Got answer:",
			line == ";; OPT PSEUDOSECTION:",
			strings.HasPrefix(line, "; EDNS:"),
			strings.HasPrefix(line, ";; WHEN:"),
			strings.HasPrefix(line, ";; MSG SIZE "):
			dropped++
			continue
		}
		if strings.HasPrefix(line, ";; ->>HEADER<<-") {
			sawHeader = true
		}
		if line == ";; QUESTION SECTION:" {
			sawQuestion = true
		}
		kept = append(kept, line)
	}
	if !sawHeader || !sawQuestion || dropped == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept = append(kept, fmt.Sprintf("…+%d dig metadata lines", dropped))
	body := strings.Join(kept, "\n")
	if body == "" || len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body), Note: fmt.Sprintf("dig: filtered %d metadata lines", dropped), Elided: true,
	}, nil
}

func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
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
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}
