// Package pkgmgr formats the transaction output of the Linux system package
// managers: apt/apt-get, dnf/yum, apk and pacman.
//
// WHAT IS DROPPED IS ONLY WHAT IS PROVABLY PROGRESS. An install prints one
// "Get:", one "Selecting", one "Preparing", one "Unpacking" and one "Setting up"
// line per package; a 25-package install is ~12 KB of which the answer is the
// package list and the summary. Those lines are matched by anchored patterns,
// counted per category and reported once as `…+N lines elided: Get ×25, …`.
// Every line no pattern recognises is kept verbatim, so errors (E:, dpkg:
// error processing, No match for argument, ERROR:, error:), warnings and any
// text from a version of the tool this package has never seen survive by
// construction rather than by an error whitelist.
//
// Interactivity is not this package's concern: a prompt-bearing invocation
// (no -y / --noconfirm) is excluded from wrapping by the hook's streamsForever,
// because wrapping buffers stdout to EOF and the prompt would never be seen.
package pkgmgr

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders one package manager program.
type Formatter struct{ prog string }

// All returns one formatter per supported program.
func All() []format.Formatter {
	var fs []format.Formatter
	for _, p := range []string{"apt", "apt-get", "dnf", "yum", "apk", "pacman"} {
		fs = append(fs, &Formatter{prog: p})
	}
	return fs
}

// Descriptor claims every invocation of the program; unsupported subcommands
// decline at render time.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.prog} }

// Aggressive compacts package lists and drops progress.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, true)
}

// Relaxed only drops progress lines; package blocks stay as printed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	op, ok := operation(f.prog, in.Argv)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr := readAll(in.Stdout), readAll(in.Stderr)
	raw := len(stdout) + len(stderr)
	if raw == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := splitLines(stdout)
	lines = append(lines, splitLines(stderr)...)

	a := &acc{}
	switch f.prog {
	case "apt", "apt-get":
		if op == "list" {
			renderAptList(a, lines, aggressive)
		} else {
			renderApt(a, lines, aggressive)
		}
	case "dnf", "yum":
		renderRPM(a, lines, aggressive, op)
	case "apk":
		renderApk(a, lines, aggressive)
	case "pacman":
		renderPacman(a, lines, aggressive)
	}
	failed := in.ExitCode != 0 && !(op == "check-update" && in.ExitCode == 100)
	body := a.String(failed)
	if raw-len(body) < minSaving {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:       []byte(body),
		FoldStderr: len(stderr) > 0,
		Elided:     a.elided() > 0,
	}, nil
}

// minSaving is the fewest bytes a render must save to be worth replacing the
// raw output; a one-byte win (a trimmed trailing newline) is not coverage.
const minSaving = 16

// operation extracts the verb this formatter understands, or ok=false when the
// invocation is one it must leave alone (searches, queries, machine output).
func operation(prog string, argv []string) (string, bool) {
	if len(argv) < 2 {
		return "", false
	}
	args := argv[1:]
	for _, a := range args {
		switch a {
		case "--print-uris", "--json", "--print", "-p", "--version", "-V", "--help", "-h":
			return "", false
		}
	}
	if prog == "pacman" {
		for _, a := range args {
			if len(a) < 2 || a[0] != '-' || a[1] == '-' {
				continue
			}
			switch a[1] {
			case 'S':
				// -Ss/-Si/-Sl/-Sg are searches and listings, not transactions.
				if !strings.ContainsAny(a[2:], "silg") {
					return "pacman", true
				}
			case 'R', 'U':
				return "pacman", true
			}
		}
		return "", false
	}
	verb := firstOperand(args)
	switch prog {
	case "apt", "apt-get":
		switch verb {
		case "update", "install", "upgrade", "dist-upgrade", "full-upgrade", "remove", "purge", "autoremove", "autopurge", "reinstall":
			return verb, true
		case "list":
			if prog == "apt" {
				return verb, true
			}
		}
	case "dnf", "yum":
		switch verb {
		case "install", "update", "upgrade", "remove", "erase", "autoremove", "reinstall", "downgrade",
			"distro-sync", "check-update", "makecache", "check", "clean", "swap":
			return verb, true
		}
	case "apk":
		switch verb {
		case "add", "del", "update", "upgrade", "fix":
			return verb, true
		}
	}
	return "", false
}

// optsWithValue are options of the apt/dnf family that consume the next token.
var optsWithValue = map[string]bool{
	"-o": true, "-c": true, "-t": true, "-a": true, "--repo": true, "--enablerepo": true,
	"--disablerepo": true, "-x": true, "--exclude": true, "--setopt": true, "-C": true,
}

func firstOperand(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if optsWithValue[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
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
		// progress meters redraw with \r; only the last frame is the state
		if j := strings.LastIndex(l, "\r"); j >= 0 && j < len(l)-1 {
			l = l[j+1:]
		}
		lines[i] = strings.TrimRight(l, "\r ")
	}
	return lines
}

// acc accumulates kept lines and counted noise.
type acc struct {
	out    []string
	counts map[string]int
	order  []string
	last   string
}

func (a *acc) keep(l string) { a.out = append(a.out, l) }

func (a *acc) drop(cat, line string) {
	if a.counts == nil {
		a.counts = map[string]int{}
	}
	if _, ok := a.counts[cat]; !ok {
		a.order = append(a.order, cat)
	}
	a.counts[cat]++
	if line != "" && stepCats[cat] {
		a.last = line
	}
}

// stepCats are the categories whose last line names the step a failure
// happened in; reading-the-package-list noise says nothing about it.
var stepCats = map[string]bool{"Preparing": true, "Unpacking": true, "Setting up": true, "Removing": true, "triggers": true, "steps": true, "hooks": true}

func (a *acc) elided() int {
	n := 0
	for _, c := range a.counts {
		n += c
	}
	return n
}

// String renders the kept lines then one elision marker. On a failed run the
// last dropped progress line is kept, because it names the step that failed.
func (a *acc) String(failed bool) string {
	out, _ := collapse.Runs(a.out)
	if failed && a.last != "" {
		out = append(out, "last step: "+strings.TrimSpace(a.last))
	}
	if n := a.elided(); n > 0 {
		parts := make([]string, 0, len(a.order))
		for _, c := range a.order {
			parts = append(parts, fmt.Sprintf("%s ×%d", c, a.counts[c]))
		}
		out = append(out, fmt.Sprintf("…+%d lines elided: %s", n, strings.Join(parts, ", ")))
	}
	return strings.Join(out, "\n")
}

// squeeze collapses runs of spaces to one. It is applied to table rows, whose
// column padding carries no information.
func squeeze(l string) string {
	return strings.Join(strings.Fields(l), " ")
}
