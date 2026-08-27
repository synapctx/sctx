package gotest

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// diffMode reports whether this `go fix` invocation was asked for a patch
// rather than for the fixes to be applied.
//
// WHY EVERY TIER MUST DECLINE FOR IT. `go fix -diff` emits a unified diff, and
// a unified diff is the answer, not a report about one. It also cannot survive
// relaxedFilter: a context line for a BLANK source line is a single space, which
// that filter drops as whitespace, and consecutive identical context lines
// collapse to "line ×N". Either one silently produces a patch that no longer
// describes the change — the one output shape where "smaller" and "wrong" are
// the same edit.
func diffMode(in format.Input) bool {
	for _, a := range in.Argv {
		if i := strings.IndexByte(a, '='); i > 0 {
			a = a[:i]
		}
		if a == "-diff" || a == "--diff" {
			return true
		}
	}
	return false
}

// fixSummary matches the per-package line `go fix` prints when it applies
// fixes, e.g. "fix: applied 7 of 8 fixes; 3 files updated. (Re-run ...)".
var fixSummary = regexp.MustCompile(`^fix: applied (\d+) of (\d+) fixes; (\d+) files? updated\.`)

// aggressiveFix renders `go fix` in APPLY mode. Its output is one summary line
// per package plus the "# package" banners that precede them, which over a
// module with many packages is almost entirely repetition of a number the
// caller wants totalled anyway.
//
// The re-run notice is preserved as its own line and never folded into the
// totals. `go fix` applies only non-overlapping edits in a single pass, so a
// package reporting "applied 7 of 8" has fixes still pending; treating that as
// done is how a migration silently half-lands. Measured on this repository: three
// modules needed five further passes before converging.
func aggressiveFix(in format.Input) (format.Rendered, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("gotest: reading stderr: %w", err)
	}
	if len(stdout) == 0 && len(stderr) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	// go fix reports on stderr; read both so neither stream is assumed.
	lines := append(splitLines(string(stdout)), splitLines(string(stderr))...)

	var applied, offered, files, pkgs, incomplete int
	var kept []string
	matched := false

	for _, l := range lines {
		if m := fixSummary.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			matched = true
			pkgs++
			a, o, f := atoiSafe(m[1]), atoiSafe(m[2]), atoiSafe(m[3])
			applied += a
			offered += o
			files += f
			if a < o {
				incomplete++
			}
			continue
		}
		// "# package" banners carry no information the totals do not.
		if strings.HasPrefix(l, "# ") {
			continue
		}
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}

	// Nothing recognizable: let a later tier decide rather than inventing a summary.
	if !matched {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var b strings.Builder
	for _, l := range kept {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "fix: applied %d of %d fixes across %d packages; %d files updated",
		applied, offered, pkgs, files)
	if incomplete > 0 {
		fmt.Fprintf(&b, "\n%d package(s) have fixes left: re-run until it reports 0 remaining "+
			"(one pass applies only non-overlapping edits)", incomplete)
	}
	b.WriteByte('\n')

	return format.Rendered{Body: []byte(b.String()), FoldStderr: len(stderr) > 0}, nil
}

// atoiSafe converts a regexp-captured run of digits; a non-number is impossible
// given the pattern, so a failure simply contributes nothing to the total.
func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
