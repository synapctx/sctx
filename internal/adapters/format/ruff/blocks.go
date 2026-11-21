package ruff

import (
	"regexp"
	"strings"
)

// arrowRe matches the location line of ruff's full (default) diagnostic
// format: " --> pkg/core.py:12:5".
var arrowRe = regexp.MustCompile(`^\s*--> (.+?):(\d+):(\d+)$`)

// codeHeaderRe matches the first line of a full-format diagnostic:
// "F401 [*] `os` imported but unused", "F821 Undefined name `x`",
// "invalid-syntax: Expected an expression" or "unformatted: File would be
// reformatted". The code is a rule id or a lower-case kebab-case kind.
var codeHeaderRe = regexp.MustCompile(`^([A-Z]{1,6}\d{1,4}|[a-z]+(?:-[a-z]+)*:)(?: (\[\*\]))? ?(.*)$`)

// item is one parsed unit of ruff output: either a diagnostic block (header
// line, location arrow, then source/diff snippet up to a blank line) or a
// plain line outside any block.
type item struct {
	// plain is set for a line that belongs to no block.
	plain string
	// block fields.
	isBlock         bool
	code, msg       string
	fixable         bool
	file, line, col string
	// dropped counts the snippet/help lines dropped from the block.
	dropped int
}

// parseItems splits ruff output into blocks and plain lines. A block starts
// at a header line that is immediately followed by an arrow line and runs
// until the next blank line.
func parseItems(lines []string) []item {
	var out []item
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if i+1 < len(lines) {
			h := codeHeaderRe.FindStringSubmatch(l)
			a := arrowRe.FindStringSubmatch(lines[i+1])
			if h != nil && a != nil {
				it := item{
					isBlock: true,
					code:    strings.TrimSuffix(h[1], ":"),
					fixable: h[2] != "",
					msg:     h[3],
					file:    a[1], line: a[2], col: a[3],
				}
				j := i + 2
				for j < len(lines) && strings.TrimSpace(lines[j]) != "" {
					it.dropped++
					j++
				}
				out = append(out, it)
				i = j - 1
				continue
			}
		}
		out = append(out, item{plain: l})
	}
	return out
}

// hasFlag reports whether argv carries the flag (exact, or as --flag=value).
func hasFlag(argv []string, flags ...string) bool {
	for _, a := range argv {
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

// userShapedOutput reports flags whose output is the user's chosen data
// (a diff, statistics table, JSON, settings dump) and must not be reshaped.
func userShapedOutput(argv []string) bool {
	if hasFlag(argv, "--diff", "--statistics", "--show-settings", "--show-files", "--show-source") {
		return true
	}
	for i, a := range argv {
		if a == "--output-format" && i+1 < len(argv) && argv[i+1] != "full" && argv[i+1] != "concise" {
			return true
		}
		if strings.HasPrefix(a, "--output-format=") && a != "--output-format=full" && a != "--output-format=concise" {
			return true
		}
	}
	return false
}
