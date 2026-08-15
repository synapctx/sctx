package hook

import (
	"strings"
	"testing"
)

// TestNewlineInsideQuotesIsText is the regression for a bug that shipped and immediately
// corrupted a real command. `ssh host 'a\nls -l'` was split at the newline INSIDE the
// single-quoted argument, sctx was inserted into the remote command, and it failed on the
// remote host with "sctx: command not found".
//
// The fuzzer did not catch it because its invariant — stripping insertions reproduces the
// input — is satisfied by an insertion inside a quoted string. It now also asserts every
// insertion offset is unquoted.
func TestNewlineInsideQuotesIsText(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			// The exact shape that broke. The shared nested-command grammar now declines
			// multi-command remote scripts entirely: one formatter cannot own both outputs.
			// The quoted argument still has to come back byte-identical.
			"ssh with a multi-line single-quoted script",
			"ssh host 'mkdir -p /tmp/x\nls -l /tmp/x'",
			"ssh host 'mkdir -p /tmp/x\nls -l /tmp/x'",
		},
		{
			// A table program with a quoted multi-line argument: the program itself may
			// wrap, but nothing inside the quotes may.
			"git commit with a multi-line message",
			"git commit -m 'line one\ngo test ./...'",
			"sctx git commit -m 'line one\ngo test ./...'",
		},
		{
			"double-quoted multi-line argument",
			"git commit -m \"first\nls -la\"",
			"sctx git commit -m \"first\nls -la\"",
		},
		{
			// And a genuine top-level newline still separates.
			"unquoted newline still separates",
			"git status\ngo vet ./...",
			"sctx git status\nsctx go vet ./...",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := rewrite(tc.in)
			if got != tc.want {
				t.Errorf("rewrite(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestQuotedRegionsAreByteIdentical states the real invariant, independent of whether any
// particular program happens to be in the rewrite table.
//
// The exact-string test above has to be edited whenever coverage changes — adding ssh to the
// table flipped one of its expectations — and an expectation that gets edited to match new
// behaviour is one that stops protecting anything. This asserts the property instead: sctx
// may be inserted before a program, never inside a quoted argument, so every quoted region
// of the input must survive unchanged in the output.
func TestQuotedRegionsAreByteIdentical(t *testing.T) {
	for _, in := range []string{
		"ssh host 'mkdir -p /tmp/x\nls -l /tmp/x'",
		"ssh host 'cd /opt && go test ./...'",
		"git commit -m 'line one\ngo test ./...'",
		"git commit -m \"first\nls -la\"",
		"rsync -av 'src dir/' 'dst dir/'",
		"ssh vm 'docker ps; ls -l'",
		"git log --format='%h go test ./...'",
	} {
		got, ok := rewrite(in)
		if !ok {
			continue // declined: nothing was inserted, so nothing can have been corrupted
		}
		for _, q := range quotedRegions(in) {
			if !strings.Contains(got, q) {
				t.Errorf("rewrite(%q) = %q\n  quoted region %q did not survive byte-identical — sctx was inserted inside a quoted argument", in, got, q)
			}
		}
	}
}

// quotedRegions returns each single- or double-quoted span of s, quotes included.
func quotedRegions(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\'' && c != '"' {
			continue
		}
		if j := indexByteFrom(s, c, i+1); j > i {
			out = append(out, s[i:j+1])
			i = j
		}
	}
	return out
}

func indexByteFrom(s string, c byte, from int) int {
	if i := strings.IndexByte(s[from:], c); i >= 0 {
		return from + i
	}
	return -1
}
