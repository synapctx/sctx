package hook

import (
	"strings"
	"testing"
)

// FuzzRewrite guards the one true hazard in rewrite/splitSegments: inserting
// "sctx " somewhere that corrupts the command, most dangerously inside a
// quoted string literal. It asserts three properties that must hold for
// every input, seeded from every hand-picked rewriteTestCases row plus a
// handful of adversarial quote/escape/separator combinations:
//
//  1. rewrite never panics.
//  2. When it reports a rewrite, the result is cmd with exactly one
//     "sctx " inserted (length grows by exactly 5, and there is exactly one
//     split point i such that got == cmd[:i]+"sctx "+cmd[i:]).
//  3. Rewriting the result again is a no-op (idempotence) — sctx never
//     double-wraps a command it just wrapped.
func FuzzRewrite(f *testing.F) {
	for _, tt := range rewriteTestCases() {
		f.Add(tt.cmd)
	}
	// Adversarial quote/escape/separator combinations not already covered
	// by the table, aimed squarely at the quote-aware scanner.
	seeds := []string{
		`go test "` + "`" + `"`,
		`go test '$('`,
		`go test "a\"b" && go vet`,
		`go test 'a\'\''b'`,
		`go test \\`,
		`go test ";" | head`,
		`go test '|' && ls`,
		`"go test" && echo hi`,
		`go test 2>&1 2>&1 | tail`,
		`go test |`,
		`go test &&&&`,
		``,
		`   `,
		`;;;`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, cmd string) {
		got, ok := rewrite(cmd)
		if !ok {
			if got != cmd {
				t.Fatalf("rewrite(%q) declined but returned %q, want unchanged input", cmd, got)
			}
			return
		}

		// A rewrite inserts "sctx " before EVERY eligible segment, so growth is a
		// positive multiple of the prefix length. The invariant used to be "exactly
		// one insertion"; that encoded the old single-wrap behaviour, and the fuzzer
		// caught the change immediately, which is what it is for.
		grew := len(got) - len(cmd)
		if grew <= 0 || grew%len("sctx ") != 0 {
			t.Fatalf("rewrite(%q) = %q, length grew by %d, want a positive multiple of %d",
				cmd, got, grew, len("sctx "))
		}

		// Removing the inserted prefixes must reproduce the input exactly: sctx only
		// ever ADDS its own token, never edits, reorders or drops a byte.
		if stripped := strings.ReplaceAll(got, "sctx ", ""); stripped != strings.ReplaceAll(cmd, "sctx ", "") {
			t.Fatalf("rewrite(%q) = %q; stripping insertions gives %q, want the original command back",
				cmd, got, stripped)
		}

		// AND EVERY INSERTION MUST BE OUTSIDE QUOTES. The check above is satisfied by an
		// insertion in the middle of a quoted string, which is the one true hazard this
		// fuzzer exists to guard — and it missed it: a misplaced newline branch split
		// inside `ssh host 'a\nls -l'` and inserted sctx into the remote command.
		// A heredoc BODY is literal text, not shell, so it is both (a) a place no
		// insertion may ever land and (b) a region whose quotes are not quotes — an
		// unbalanced `"` in a body must not make the rest of the command look quoted.
		bodies := heredocBodyRanges(cmd)
		for _, off := range insertionOffsets(cmd, got) {
			if inSpans(bodies, off) {
				t.Fatalf("rewrite(%q) = %q inserted at offset %d, which is inside a heredoc body",
					cmd, got, off)
			}
			if quotedAt(cmd, off, bodies) {
				t.Fatalf("rewrite(%q) = %q inserted at offset %d, which is inside a quoted string",
					cmd, got, off)
			}
		}

		got2, ok2 := rewrite(got)
		if ok2 {
			t.Fatalf("rewrite(%q) [already rewritten] = (%q, true), want (%q, false) — not idempotent", got, got2, got)
		}
		if got2 != got {
			t.Fatalf("rewrite(%q) [already rewritten] returned %q, want unchanged", got, got2)
		}
	})
}

// insertionOffsets recovers the offsets in the ORIGINAL command at which "sctx " was
// inserted, by walking both strings together.
func insertionOffsets(cmd, got string) []int {
	var offs []int
	i, j := 0, 0
	for i < len(cmd) && j < len(got) {
		if cmd[i] == got[j] {
			i++
			j++
			continue
		}
		if strings.HasPrefix(got[j:], "sctx ") {
			offs = append(offs, i)
			j += len("sctx ")
			continue
		}
		// Any other divergence is a different bug, caught by the strip check above.
		return offs
	}
	if j < len(got) && strings.HasPrefix(got[j:], "sctx ") {
		offs = append(offs, i)
	}
	return offs
}

type span struct{ lo, hi int } // [lo, hi)

func inSpans(spans []span, off int) bool {
	for _, s := range spans {
		if off >= s.lo && off < s.hi {
			return true
		}
	}
	return false
}

// heredocBodyRanges returns the byte spans of cmd that are heredoc BODY (including each
// terminator line, which is no more shell than the body it closes).
//
// This is deliberately a SEPARATE, line-based implementation rather than a call into
// heredoc.go: an oracle that shares the code under test only ever agrees with it. It is
// the naive reading of the feature — collect the delimiters a line opens, then treat
// following lines as opaque until each one is closed — which is exactly the reading the
// scanner must match.
func heredocBodyRanges(cmd string) []span {
	var spans []span
	// A delimiter records whether it came from `<<-`: tab-stripping applies to THAT form
	// only. Stripping unconditionally terminated a plain `<<0` body on the line `\t0`,
	// one line early, and the real body text after it was then read as shell.
	type delim struct {
		word string
		dash bool
	}
	var pending []delim // delimiters awaiting a terminator, in order
	var inSingle, inDouble, escaped bool
	// continued means the previous line ended in a `\` that consumed its newline, so the
	// next line is still the SAME shell line. It decides where a heredoc BODY begins: in
	// `<<0 \\\n0\n"000\n0\nls` the start line is `<<0 0` and the body starts at `"000`,
	// but a line-at-a-time oracle popped the delimiter on the continuation line `0` and
	// then read the body as shell — reporting a quote that bash never opens.
	continued := false

	for pos := 0; pos < len(cmd); {
		end := strings.IndexByte(cmd[pos:], '\n')
		lineEnd := len(cmd)
		if end >= 0 {
			lineEnd = pos + end
		}
		line := cmd[pos:lineEnd]

		if len(pending) > 0 && !continued {
			// Opaque line. Include the trailing newline so an insertion at the very
			// start of the next body line is still reported inside a body.
			hi := lineEnd
			if end >= 0 {
				hi++
			}
			spans = append(spans, span{pos, hi})
			if line == pending[0].word || (pending[0].dash && strings.TrimLeft(line, "\t") == pending[0].word) {
				pending = pending[1:]
			}
		} else {
			// Shell line: collect the heredoc delimiters it opens. Quote state carries
			// across shell lines, because a quoted string may span newlines.
			//
			// The NEWLINE is part of what is scanned. Without it, a trailing `\` left
			// escaped=true and ate the FIRST CHARACTER OF THE NEXT LINE instead of the
			// newline — so in `\\\n'<<'0\ngo test` the `'` was read as escaped, the `<<`
			// looked unquoted, and a phantom heredoc swallowed the rest. bash treats
			// `\`+newline as a line continuation and runs the command `<<0`, verified
			// directly.
			line := line
			if end >= 0 {
				line += "\n"
			}
			continued = false
			for i := 0; i < len(line); i++ {
				c := line[i]
				switch {
				case escaped:
					escaped = false
					if c == '\n' {
						continued = true // `\`+newline: the command carries on
					}
				case c == '\\' && !inSingle:
					escaped = true
				case c == '\'' && !inDouble:
					inSingle = !inSingle
				case c == '"' && !inSingle:
					inDouble = !inDouble
				case c == '<' && !inSingle && !inDouble && i+1 < len(line) && line[i+1] == '<':
					j := i + 2
					if j < len(line) && line[j] == '<' {
						i = j // `<<<` is a here-STRING: no body follows.
						continue
					}
					dash := false
					if j < len(line) && line[j] == '-' {
						dash = true
						j++
					}
					for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
						j++
					}
					q := byte(0)
					if j < len(line) && (line[j] == '\'' || line[j] == '"') {
						q = line[j]
						j++
					}
					start := j
					for j < len(line) {
						if q != 0 {
							if line[j] == q {
								break
							}
						} else if line[j] == ' ' || line[j] == '\t' || line[j] == '\n' || line[j] == ';' ||
							line[j] == '|' || line[j] == '&' || line[j] == '>' || line[j] == '<' {
							break
						}
						j++
					}
					if d := line[start:j]; d != "" {
						pending = append(pending, delim{word: d, dash: dash})
					}
					if q != 0 && j < len(line) {
						j++ // past the CLOSING quote, or it re-opens the quote below
					}
					i = j - 1
				}
			}
		}

		if end < 0 {
			break
		}
		pos = lineEnd + 1
	}
	return spans
}

// quotedAt reports whether byte offset off in cmd sits inside a single- or double-quoted
// region, using the same escape rules as splitSegments, skipping heredoc bodies (whose
// quote characters are literal text).
func quotedAt(cmd string, off int, bodies []span) bool {
	var inSingle, inDouble, escaped bool
	for i := 0; i < off && i < len(cmd); i++ {
		if inSpans(bodies, i) {
			continue
		}
		c := cmd[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && !inSingle {
			escaped = true
			continue
		}
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if c == '"' && !inSingle {
			inDouble = !inDouble
		}
	}
	return inSingle || inDouble
}
