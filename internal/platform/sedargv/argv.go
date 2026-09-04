// Package sedargv shares the recognition grammar for the two sed
// invocation shapes that are actually a READ (an untransformed subset of a
// file's own lines) rather than a stream-editing transform: both the hook's
// wrap decision and the formatter's own recognition guard must agree on
// exactly which shapes are safe, or one buffers what the other refuses to
// render. Verified against the real darwin `sed` (BSD sed — `-n` only, no
// GNU long options).
package sedargv

import (
	"regexp"
	"strings"
)

// rangeAddress matches a numeric line-range print expression, e.g. "5,10p".
var rangeAddress = regexp.MustCompile(`^[0-9]+,[0-9]+p$`)

// regexAddress matches a single-pattern print expression, e.g. "/needle/p".
// Deliberately simple (no escaped slashes, no flags): sed's regex address
// grammar is otherwise open-ended, and a shape this cannot recognise with
// confidence must decline rather than guess.
var regexAddress = regexp.MustCompile(`^/[^/]*/p$`)

// RecognizedRead reports whether argv is exactly `sed -n EXPR FILE`: `-n`
// first, one of the two safe print expressions, then a single file argument
// that is not itself a flag. Any other shape — a substitution, `-i`,
// multiple files, a script file, an unrecognised address — declines.
func RecognizedRead(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	if argv[1] != "-n" {
		return false
	}
	expr := argv[2]
	if !rangeAddress.MatchString(expr) && !regexAddress.MatchString(expr) {
		return false
	}
	file := argv[3]
	return file != "" && !strings.HasPrefix(file, "-")
}
