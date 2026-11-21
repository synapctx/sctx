// Package netinfo formats the Linux network inspection commands: `ip addr`,
// `ip link` (full and -br forms), `ss`, `netstat` and `traceroute`.
//
// Interface listings are re-rendered one interface per line, dropping fields
// that are constant noise (qdisc, group, qlen, valid_lft forever, brd addresses
// derivable from the prefix) and folding down/addressless interfaces into one
// counted line. Socket tables are whitespace-squeezed (the column padding is a
// large fraction of the bytes) and, once a host has many established
// connections, grouped by endpoint with a count. Listening sockets are never
// grouped: each one is a distinct fact.
//
// Machine and detail output declines: `-j`/`-json`, `-o`/`-oneline`, `-s` and
// `-d` on ip; `-i`, `-m`, `-o`, `-e`, `-H` and `-c` on ss.
package netinfo

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

const minSaving = 16

// Formatter renders one network program.
type Formatter struct{ prog string }

// All returns one formatter per supported program.
func All() []format.Formatter {
	var fs []format.Formatter
	for _, p := range []string{"ip", "ss", "netstat", "traceroute"} {
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

// Relaxed only squeezes column padding and folds identical lines.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

// result is one handler's render.
type result struct {
	out    []string
	elided int
	ok     bool
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) < 2 && f.prog != "ss" && f.prog != "netstat" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := readAll(in.Stdout), readAll(in.Stderr)
	raw := len(stdout) + len(stderr)
	if raw == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := splitLines(stdout)
	errLines := splitLines(stderr)

	var res result
	switch f.prog {
	case "ip":
		res = renderIP(in.Argv, lines, aggressive)
	case "ss":
		res = renderSockets(in.Argv, lines, aggressive, "ss")
	case "netstat":
		res = renderSockets(in.Argv, lines, aggressive, "netstat")
	case "traceroute":
		res = renderTraceroute(in.Argv, lines, aggressive)
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

func hasAny(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

// shortCluster reports whether a single-dash option cluster such as -tulpn
// contains any of the letters.
func shortCluster(args []string, letters string) bool {
	for _, a := range args {
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters) {
			return true
		}
	}
	return false
}
