// Package systemd formats `systemctl` (status, list-units, list-unit-files,
// --failed) and `journalctl` output.
//
// The two share one journal-line collapser because they print the same
// grammar: `systemctl status` ends with the last ten lines of the unit's
// journal. What it compresses, and why each step is safe:
//
//   - The hostname is identical on every line of a single-host journal, so it is
//     stated once and removed from each line (lossless).
//   - A run of 3+ consecutive lines with an identical message is folded into the
//     first timestamp, the last time, and ×N (reconstructible).
//   - A run of 5+ consecutive NON-ERROR lines that differ only in digits is
//     shown as its first line, a counted marker, and its last line. This one is
//     lossy and says so ("digits vary"). Lines that look like errors are never
//     shape-folded, because `status=1` and `status=137` differ in exactly the
//     digits being masked.
//
// For `list-units` the per-unit description is dropped for healthy units, which
// are grouped by (state, unit type); every unit that is failed, activating,
// not-found or otherwise not `loaded active` keeps its full row.
package systemd

import (
	"context"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// minSaving is the fewest bytes a render must save to replace the raw output.
const minSaving = 16

// Formatter renders one systemd program.
type Formatter struct{ prog string }

// All returns the systemctl and journalctl formatters.
func All() []format.Formatter {
	return []format.Formatter{&Formatter{prog: "systemctl"}, &Formatter{prog: "journalctl"}}
}

// Descriptor claims every invocation; unsupported subcommands decline.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.prog} }

// Aggressive is the full compaction.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, true)
}

// Relaxed only folds exactly identical lines.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return f.render(in, false)
}

func (f *Formatter) render(in format.Input, aggressive bool) (format.Rendered, error) {
	var (
		verb string
		ok   bool
	)
	if f.prog == "systemctl" {
		verb, ok = systemctlVerb(in.Argv)
	} else {
		ok = journalctlRenderable(in.Argv)
	}
	if !ok {
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
	switch {
	case f.prog == "journalctl":
		res = renderJournal(lines, aggressive)
	case verb == "status":
		res = renderStatus(lines, aggressive)
	case verb == "list-units":
		res = renderListUnits(lines, aggressive)
	case verb == "list-unit-files":
		res = renderListUnitFiles(lines, aggressive)
	}
	if !res.ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// stderr carries diagnostics ("Failed to ...", "Unit x could not be found");
	// it is appended verbatim, never summarised.
	out := append(res.out, errLines...)
	body := strings.Join(out, "\n")
	if raw-len(body) < minSaving {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(errLines) > 0, Elided: res.elided > 0}, nil
}

// result is one verb's render.
type result struct {
	out    []string
	elided int
	ok     bool
}

// systemctlVerb returns the supported operation. `--failed` with no verb is
// `list-units --state=failed`.
func systemctlVerb(argv []string) (string, bool) {
	if len(argv) < 2 {
		return "", false
	}
	args := argv[1:]
	verb := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-o", "--output", "-H", "--host", "-M", "--machine", "-t", "--type", "-p", "--property", "-s", "--signal", "--state", "-n", "--lines", "-P", "--value":
			i++
			continue
		case "--json", "--json=short", "--json=pretty", "--json=off":
			return "", false
		}
		if strings.HasPrefix(a, "--json") || strings.HasPrefix(a, "--output=json") || strings.HasPrefix(a, "-o=json") {
			return "", false
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		verb = a
		break
	}
	if j := optValue(args, "-o", "--output"); strings.HasPrefix(j, "json") || j == "export" || j == "verbose" {
		return "", false
	}
	if verb == "" {
		for _, a := range args {
			if a == "--failed" {
				return "list-units", true
			}
		}
		return "", false
	}
	switch verb {
	case "status", "list-units", "list-unit-files":
		return verb, true
	}
	return "", false
}

// journalctlRenderable declines machine output and the non-log modes.
func journalctlRenderable(argv []string) bool {
	args := argv[1:]
	if j := optValue(args, "-o", "--output"); strings.HasPrefix(j, "json") || j == "export" || j == "verbose" {
		return false
	}
	for _, a := range args {
		switch {
		case a == "--disk-usage", a == "--list-boots", a == "--header", a == "--verify", a == "--fields", a == "-N",
			a == "--field", a == "-F", a == "--flush", a == "--rotate", a == "--sync", a == "--update-catalog",
			strings.HasPrefix(a, "--vacuum"), strings.HasPrefix(a, "--field="), strings.HasPrefix(a, "--cursor-file"):
			return false
		}
	}
	return true
}

// optValue returns the value of a short/long option given as `-o v`, `-ov`,
// `--output v` or `--output=v`.
func optValue(args []string, short, long string) string {
	for i, a := range args {
		switch {
		case a == short || a == long:
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, long+"="):
			return strings.TrimPrefix(a, long+"=")
		case strings.HasPrefix(a, short) && len(a) > len(short) && !strings.HasPrefix(a, "--"):
			if a[len(short)] == '=' {
				return a[len(short)+1:]
			}
			if short == "-o" {
				return a[len(short):]
			}
		}
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
		lines[i] = strings.TrimRight(l, "\r")
	}
	return lines
}
