// Package jsutil holds the small helpers shared by the JavaScript/TypeScript
// tool formatters (tsc, eslint, jstest, npx): stream reading, ANSI stripping,
// line splitting and the "name=count" histogram.
package jsutil

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// StripANSI removes terminal colour/cursor escapes. Every JS tool colours its
// output when it believes it has a terminal, and parsing must not depend on it.
func StripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// ReadAll drains a possibly-nil reader.
func ReadAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

// Streams reads stdout and stderr of in.
func Streams(in format.Input) (stdout, stderr []byte) {
	return ReadAll(in.Stdout), ReadAll(in.Stderr)
}

// Lines splits raw text into ANSI-stripped lines without trailing whitespace.
func Lines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(StripANSI(sc.Text()), " \t\r"))
	}
	return lines
}

// Plural picks the singular form for exactly one.
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// Histogram renders "label: a=3 b=2", most frequent first, ties by name.
func Histogram(label string, counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	var b strings.Builder
	b.WriteString(label + ":")
	for _, n := range names {
		fmt.Fprintf(&b, " %s=%d", n, counts[n])
	}
	return b.String()
}

// IsUserPath reports whether a stack-frame path belongs to the user's code
// rather than node_modules or the runtime.
func IsUserPath(p string) bool {
	if p == "" {
		return false
	}
	return !strings.Contains(p, "node_modules") &&
		!strings.HasPrefix(p, "node:") &&
		!strings.HasPrefix(p, "internal/") &&
		!strings.HasPrefix(p, "<anonymous>")
}
