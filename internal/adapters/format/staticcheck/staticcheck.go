// Package staticcheck formats the default text report of `staticcheck`. Each
// finding is `path:line:col: message (CHECK)`; the aggressive tier states the
// path once per file and keeps every line:col, message and check code, which
// is lossless. -f other than text, -explain, -list-checks, -version and the
// debug flags decline.
package staticcheck

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var findingRE = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.*)$`)

// Formatter renders staticcheck output.
type Formatter struct{}

// New constructs a staticcheck Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every staticcheck invocation.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "staticcheck"} }

// textReport reports whether argv asks for the default findings listing.
func textReport(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		a := strings.TrimLeft(argv[i], "-")
		k, v, hasVal := strings.Cut(a, "=")
		switch {
		case k == "f":
			if !hasVal && i+1 < len(argv) {
				v = argv[i+1]
			}
			if v != "text" {
				return false
			}
		case k == "explain", k == "list-checks", k == "version", k == "h", k == "help", strings.HasPrefix(k, "debug."):
			return false
		}
		if argv[i] == "--" {
			break
		}
	}
	return true
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

// Aggressive groups findings by file, preserving their order.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !textReport(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	o, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("staticcheck: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("staticcheck: reading stderr: %w", err)
	}
	lines := append(splitLines(string(o)), splitLines(string(e))...)
	var out []string
	prev := ""
	n := 0
	for _, l := range lines {
		m := findingRE.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if m == nil {
			out = append(out, l)
			prev = ""
			continue
		}
		n++
		if m[1] != prev {
			out = append(out, m[1]+":")
			prev = m[1]
		}
		out = append(out, "  "+m[2]+":"+m[3]+" "+m[4])
	}
	if n == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(out, "\n") + "\n"), FoldStderr: true}, nil
}

// Relaxed declines: there is no noise in staticcheck output to drop.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
