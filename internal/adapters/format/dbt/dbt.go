// Package dbt compacts the console log of dbt Core (`dbt run`, `test`, `build`,
// `seed`, `snapshot`, `compile`, `deps`, `debug`, `parse`). Every log line
// carries an ANSI-coloured timestamp, and each model contributes a START line
// plus a result line, so a 200-model project is hundreds of lines whose only
// signal is "all fine". The formatter strips colour and timestamps, drops the
// START lines, and replaces the per-node "N of M OK/PASS ..." lines with one
// counted summary line ("OK ×13"). Everything else is kept verbatim: ERROR,
// FAIL, WARN and SKIP result lines (SKIP beyond skipCap is counted), the whole
// failure report with its message and "compiled code at ..." path, the
// "Completed ..." line and the "Done. PASS=.. WARN=.. ERROR=.." summary.
// Exit status is never consulted to hide anything: a failed run keeps every
// line a passing run would not have dropped.
package dbt

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// skipCap SKIP result lines are kept; the rest are counted.
const skipCap = 10

var (
	ansiRE     = regexp.MustCompile("\x1b\\[[0-9;]*m")
	stampRE    = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}( {1,2}|$)`)
	resultRE   = regexp.MustCompile(`^(\d+) of (\d+) (START|OK|PASS|WARN|ERROR|FAIL|SKIP|NO-OP|REUSED)\b`)
	noiseRE    = regexp.MustCompile(`^(Registered adapter: |Unable to do partial parsing|Concurrency: \d+ threads)`)
	subcommand = map[string]bool{
		"run": true, "test": true, "build": true, "seed": true, "snapshot": true,
		"compile": true, "deps": true, "debug": true, "parse": true, "clone": true,
	}
	// valueFlags are dbt's global options that consume the next argument.
	valueFlags = map[string]bool{
		"--profiles-dir": true, "--project-dir": true, "--target": true, "-t": true,
		"--profile": true, "--log-level": true, "--log-format": true, "--log-path": true,
		"--target-path": true, "--log-level-file": true, "--log-format-file": true,
	}
)

// Formatter renders `dbt` output.
type Formatter struct{}

// New constructs a dbt Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims the `dbt` program; subcommands are dispatched inside.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "dbt"} }

// Relaxed never applies; the aggressive tier is already line-level and lossless
// for everything it keeps.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// Subcommand returns the dbt subcommand in argv, or "" if it cannot be found.
func Subcommand(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") {
			if valueFlags[a] {
				i++
			}
			continue
		}
		return a
	}
	return ""
}

// Aggressive compacts a run's console log.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if !subcommand[Subcommand(in.Argv)] || machineLog(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("dbt: reading stdout: %w", err)
	}
	if in.Stderr != nil {
		// dbt logs to stdout; stderr (if any) is re-emitted by the pipeline.
		if _, err := io.Copy(io.Discard, in.Stderr); err != nil {
			return format.Rendered{}, fmt.Errorf("dbt: reading stderr: %w", err)
		}
	}
	body, dropped, ok := compact(string(raw))
	if !ok || len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:   []byte(body),
		Note:   fmt.Sprintf("dbt: %d per-node lines counted", dropped),
		Elided: dropped > 0,
	}, nil
}

// machineLog reports a structured log format, which is left alone.
func machineLog(argv []string) bool {
	for i, a := range argv {
		if (a == "--log-format" || a == "--log-format-file") && i+1 < len(argv) && argv[i+1] == "json" {
			return true
		}
		if a == "--log-format=json" || a == "--log-format-file=json" {
			return true
		}
	}
	return false
}

func compact(raw string) (string, int, bool) {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	out := make([]string, 0, len(lines))
	counts := map[string]int{}
	var order []string
	summaryAt := -1
	skips, skipDropped, dropped := 0, 0, 0
	for _, l := range lines {
		l = ansiRE.ReplaceAllString(l, "")
		l = strings.TrimRight(l, " \t\r")
		if strings.HasPrefix(l, "{") {
			return "", 0, false // structured log line
		}
		if loc := stampRE.FindStringIndex(l); loc != nil {
			l = strings.TrimLeft(l[loc[1]:], " ")
			if l == "" {
				continue
			}
		}
		if noiseRE.MatchString(l) {
			continue
		}
		m := resultRE.FindStringSubmatch(l)
		switch {
		case m == nil:
			out = append(out, l)
		case m[3] == "START":
			dropped++
		case m[3] == "OK" || m[3] == "PASS":
			dropped++
			if counts[m[3]] == 0 {
				order = append(order, m[3])
			}
			counts[m[3]]++
			if summaryAt < 0 {
				summaryAt = len(out)
				out = append(out, "")
			}
		case m[3] == "SKIP":
			skips++
			if skips > skipCap {
				skipDropped++
				dropped++
				continue
			}
			out = append(out, l)
		default:
			out = append(out, l)
		}
	}
	if summaryAt >= 0 {
		var parts []string
		for _, k := range order {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, counts[k]))
		}
		out[summaryAt] = strings.Join(parts, ", ") + " (per-node lines collapsed)"
	}
	if skipDropped > 0 {
		out = insertAfterLastSkip(out, fmt.Sprintf("…+%d more SKIP", skipDropped))
	}
	return strings.Join(out, "\n") + "\n", dropped, true
}

// insertAfterLastSkip puts the marker right after the last kept SKIP line.
func insertAfterLastSkip(out []string, marker string) []string {
	idx := -1
	for i, l := range out {
		if m := resultRE.FindStringSubmatch(l); m != nil && m[3] == "SKIP" {
			idx = i
		}
	}
	if idx < 0 {
		return append(out, marker)
	}
	out = append(out, "")
	copy(out[idx+2:], out[idx+1:])
	out[idx+1] = marker
	return out
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
