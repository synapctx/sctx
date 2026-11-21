// Package sysinfo formats Linux host inspection commands: `top -b`, `dmesg`,
// `mount` and `lsblk`.
//
// Nothing here is a statistical summary: every reduction is either a lossless
// squeeze of column padding, a counted fold of rows that say the same thing, or
// a marked truncation. `top` keeps its header and the first rows in the order
// top printed them, then names what was left out by command; `dmesg` shows each
// distinct message once with how many times it repeated and when it last did;
// `mount` states pseudo filesystems once and trims overlay layer lists;
// `lsblk` folds empty and loop devices.
//
// Declined on purpose: interactive and following invocations (top without -b
// and -n, dmesg -w), machine output (dmesg --json, lsblk -J/-P/-r), and any
// column selection (lsblk -o/-f), because squeezing padding is only lossless
// when every column is populated.
package sysinfo

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

const minSaving = 16

// Formatter renders one host-inspection program.
type Formatter struct{ prog string }

// All returns one formatter per supported program.
func All() []format.Formatter {
	var fs []format.Formatter
	for _, p := range []string{"top", "dmesg", "mount", "lsblk"} {
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

// Relaxed applies only the lossless rules.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

type result struct {
	out    []string
	elided int
	ok     bool
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	if len(in.Argv) == 0 {
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
	switch f.prog {
	case "top":
		res = renderTop(args, lines, aggressive)
	case "dmesg":
		res = renderDmesg(args, lines, aggressive)
	case "mount":
		res = renderMount(args, lines, aggressive)
	case "lsblk":
		res = renderLsblk(args, lines, aggressive)
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

func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || (strings.HasPrefix(f, "--") && strings.HasPrefix(a, f+"=")) {
				return true
			}
		}
	}
	return false
}

// shortCluster reports whether a single-dash cluster contains any letter.
func shortCluster(args []string, letters string) bool {
	for _, a := range args {
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters) {
			return true
		}
	}
	return false
}

func squeeze(l string) string { return strings.Join(strings.Fields(l), " ") }
