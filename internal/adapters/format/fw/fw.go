// Package fw formats firewall listings: `iptables -L` / `ip6tables -L` and
// `ufw status`.
//
// `iptables -L -n -v` repeats the same nine-column header above every chain and
// prints `-- * * 0.0.0.0/0 0.0.0.0/0` on nearly every rule, which together are
// about a third of a Docker host's ruleset. The header is stated once, empty
// chains collapse to one line, and wildcard fields are omitted from rules under
// an explicit legend (`in=`/`out=`/`src=`/`dst=` appear only when they are not
// the wildcard). No rule, counter, target or option is dropped.
//
// Deliberately not handled: `iptables -S` / `iptables-save` and `nft list
// ruleset`. They are one rule per line with no padding and no repeated header,
// so the only redundancy is two comment lines (iptables-save) or zero counters
// that are themselves information (nft); measured savings were 2%.
package fw

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

const minSaving = 16

// Formatter renders one firewall program.
type Formatter struct{ prog string }

// All returns the iptables, ip6tables and ufw formatters.
func All() []format.Formatter {
	var fs []format.Formatter
	for _, p := range []string{"iptables", "ip6tables", "ufw"} {
		fs = append(fs, &Formatter{prog: p})
	}
	return fs
}

// Descriptor claims every invocation of the program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.prog} }

// Aggressive is the full compaction.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, true)
}

// Relaxed squeezes column padding only.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

type result struct {
	out    []string
	elided int
	ok     bool
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	args := in.Argv[1:]
	stdout, stderr := readAll(in.Stdout), readAll(in.Stderr)
	raw := len(stdout) + len(stderr)
	if raw == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := splitLines(stdout)
	errLines := splitLines(stderr)

	var res result
	if f.prog == "ufw" {
		res = renderUFW(args, lines)
	} else {
		res = renderIptables(args, lines, aggressive)
	}
	if !res.ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out := append(res.out, errLines...)
	body := strings.Join(out, "\n")
	if raw-len(body) < minSaving {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(errLines) > 0, Elided: res.elided > 0}, nil
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	s := strings.TrimRight(string(raw), "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	return lines
}
