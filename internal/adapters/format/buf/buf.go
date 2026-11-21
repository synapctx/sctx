// Package buf formats the text findings of `buf lint`, `buf build`, `buf
// breaking` and `buf generate`. buf prints one `path:line:col:message` line
// per finding; the aggressive tier hoists the path into a per-file header so
// it is stated once, which is lossless, and keeps every message. Other buf
// subcommands, and every --error-format other than text, decline.
package buf

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var findingRE = regexp.MustCompile(`^(.+?):(\d+):(\d+):(.*)$`)

// Formatter renders buf finding output.
type Formatter struct{}

// New constructs a buf Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every buf invocation; dispatch is internal.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "buf"} }

func handled(argv []string) bool {
	sub := ""
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			break
		}
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "--") {
			if k == "--error-format" && v != "text" {
				return false
			}
			if k == "--help" || k == "--version" {
				return false
			}
			continue
		}
		switch a {
		case "-h", "--help", "--version":
			return false
		case "--error-format":
			if i+1 < len(argv) && argv[i+1] != "text" {
				return false
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if sub == "" {
			sub = a
		}
	}
	switch sub {
	case "lint", "build", "breaking", "generate":
		return true
	}
	return false
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

func streams(in format.Input) ([]string, error) {
	o, err := readAll(in.Stdout)
	if err != nil {
		return nil, fmt.Errorf("buf: reading stdout: %w", err)
	}
	e, err := readAll(in.Stderr)
	if err != nil {
		return nil, fmt.Errorf("buf: reading stderr: %w", err)
	}
	return append(splitLines(string(o)), splitLines(string(e))...), nil
}

// Aggressive groups consecutive findings of one file under a single header.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	if !handled(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines, err := streams(in)
	if err != nil {
		return format.Rendered{}, err
	}
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

// Relaxed declines: buf output has no noise to drop beyond what Aggressive
// restructures.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
