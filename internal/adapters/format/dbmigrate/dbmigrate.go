// Package dbmigrate compacts the console output of the Flyway and Liquibase
// database migration CLIs (`flyway migrate|info|validate|...`,
// `liquibase update|status|history|validate|...`). Both print a product
// banner on every run, wrap results in borders, and on failure repeat the same
// error detail block several times through the exception chain. The
// formatter drops the banner and marketing lines, strips table borders and
// padding (Flyway `info`, Liquibase `history`), counts a long run of Liquibase
// "Running Changeset" lines, and collapses exact repeats of an error block to a
// marked "…+N repeated" line. Errors, warnings, versions applied/pending and
// the summaries are kept verbatim, and stdout and stderr are merged into one
// stream (Liquibase logs to stderr, so its stderr is emitted first).
package dbmigrate

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// changesetCap Running Changeset lines are kept (first head, last tail); the
// middle is counted.
const (
	changesetCap  = 10
	changesetHead = 5
	changesetTail = 3
)

var (
	flywayNoise = regexp.MustCompile(`^(Flyway (OSS|Community|Teams|Enterprise|Redgate)[ A-Za-z]* Edition .* by Redgate|See release notes here: |Need more flexibility with validation rules\? Learn more: )`)
	lbNoise     = regexp.MustCompile(`^(##|Starting Liquibase at |Liquibase Version: |Liquibase (Open Source|Community|Pro|Secure)\b.* by Liquibase|For more information, please use the --log-level flag)`)
	runningRE   = regexp.MustCompile(`^Running Changeset: `)
)

// Formatter renders one migration CLI.
type Formatter struct {
	command string
	noise   *regexp.Regexp
	stderr1 bool // emit stderr before stdout
}

// All returns the flyway and liquibase formatters.
func All() []format.Formatter {
	return []format.Formatter{
		&Formatter{command: "flyway", noise: flywayNoise},
		&Formatter{command: "liquibase", noise: lbNoise, stderr1: true},
	}
}

// Descriptor claims the program; the operation is dispatched by output shape.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

// Relaxed never applies; the aggressive tier only removes noise and exact repeats.
func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// Aggressive compacts the merged console output. Machine-readable output
// (-outputType=json, --format json) is declined.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if machine(in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("%s: reading stdout: %w", f.command, err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("%s: reading stderr: %w", f.command, err)
	}
	first, second := stdout, stderr
	if f.stderr1 {
		first, second = stderr, stdout
	}
	lines := splitLines(string(first))
	lines = append(lines, splitLines(string(second))...)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out, elided := f.compact(lines)
	body := strings.Join(out, "\n") + "\n"
	if len(body) >= len(stdout)+len(stderr) || len(out) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: f.command + ": banner/borders removed", FoldStderr: true, Elided: elided}, nil
}

func machine(argv []string) bool {
	for i, a := range argv {
		switch {
		case a == "-outputType=json", a == "--output-type=json", a == "--format=json", a == "-json":
			return true
		case (a == "-outputType" || a == "--output-type" || a == "--format") && i+1 < len(argv) && argv[i+1] == "json":
			return true
		}
	}
	return false
}

func (f *Formatter) compact(lines []string) ([]string, bool) {
	var kept []string
	for _, l := range lines {
		l = strings.TrimRight(l, " \t\r")
		if l == "" || f.noise.MatchString(l) {
			continue
		}
		kept = append(kept, l)
	}
	kept = unborder(kept)
	var elided bool
	kept, elided = dedupeBlocks(kept)
	var e2 bool
	kept, e2 = capRunning(kept)
	return kept, elided || e2
}

// unborder replaces every run of box-table lines (+---+ borders and | cell |
// rows) by its rows with borders and padding removed. A run whose rows disagree
// on the number of cells is left untouched.
func unborder(lines []string) []string {
	var out []string
	for i := 0; i < len(lines); {
		if !tableLine(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && tableLine(lines[j]) {
			j++
		}
		out = append(out, compactTable(lines[i:j])...)
		i = j
	}
	return out
}

func tableLine(l string) bool {
	return border(l) || (len(l) >= 2 && l[0] == '|' && l[len(l)-1] == '|')
}

func border(l string) bool {
	if len(l) < 3 || l[0] != '+' || l[len(l)-1] != '+' {
		return false
	}
	return strings.Trim(l, "+-") == ""
}

func compactTable(block []string) []string {
	var rows []string
	pipes := -1
	for _, l := range block {
		if border(l) {
			continue
		}
		n := strings.Count(l, "|")
		if pipes >= 0 && n != pipes {
			return block
		}
		pipes = n
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(l, "|"), "|"), "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		rows = append(rows, strings.Join(parts, "|"))
	}
	return rows
}

// dedupeBlocks collapses a block that repeats itself immediately: a Liquibase
// 4-line "ERROR: Exception Details" group, or a Flyway error detail block that
// starts at a dash rule and runs to the next "Caused by:" or end.
func dedupeBlocks(lines []string) ([]string, bool) {
	var out []string
	elided := false
	for i := 0; i < len(lines); {
		if strings.HasPrefix(lines[i], "ERROR: Exception Details") {
			n := 1
			for i+n < len(lines) && strings.HasPrefix(lines[i+n], "ERROR: Exception Primary") {
				n++
			}
			block := lines[i : i+n]
			out = append(out, block...)
			i += n
			repeats := 0
			for i+n <= len(lines) && equal(lines[i:i+n], block) {
				repeats++
				i += n
			}
			if repeats > 0 {
				out = append(out, fmt.Sprintf("…+%d repeated exception block", repeats))
				elided = true
			}
			continue
		}
		if isDash(lines[i]) {
			j := i + 1
			for j < len(lines) && !isDash(lines[j]) && !strings.HasPrefix(lines[j], "Caused by:") && !strings.HasPrefix(lines[j], "ERROR:") {
				j++
			}
			block := lines[i:j]
			if k := indexBlock(out, block); k >= 0 && len(block) > 2 {
				out = append(out, "…+1 repeated error detail block")
				elided = true
			} else {
				out = append(out, block...)
			}
			i = j
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return out, elided
}

func isDash(l string) bool {
	return len(l) >= 20 && strings.Trim(l, "-") == ""
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// indexBlock finds block as a contiguous run in out, or -1.
func indexBlock(out, block []string) int {
	for i := 0; i+len(block) <= len(out); i++ {
		if equal(out[i:i+len(block)], block) {
			return i
		}
	}
	return -1
}

// capRunning keeps the first changesetHead and last changesetTail "Running
// Changeset:" lines of one consecutive run longer than changesetCap.
func capRunning(lines []string) ([]string, bool) {
	var out []string
	elided := false
	for i := 0; i < len(lines); {
		if !runningRE.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && runningRE.MatchString(lines[j]) {
			j++
		}
		run := lines[i:j]
		if len(run) > changesetCap {
			out = append(out, run[:changesetHead]...)
			out = append(out, fmt.Sprintf("…+%d Running Changeset lines", len(run)-changesetHead-changesetTail))
			out = append(out, run[len(run)-changesetTail:]...)
			elided = true
		} else {
			out = append(out, run...)
		}
		i = j
	}
	return out, elided
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
