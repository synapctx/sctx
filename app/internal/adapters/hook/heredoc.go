package hook

import "strings"

// Heredoc handling for splitSegments.
//
// Shell semantics, which the first implementation got wrong: a heredoc's START LINE is
// ordinary shell — `cat <<EOF | grep x > out.txt` has a pipe and a redirect that matter —
// and only its BODY is opaque text. Skipping from `<<` straight past the terminator made
// everything on the start line invisible, so `cat <<EOF | grep needle` wrapped `cat` and
// let grep filter sctx's compressed output. That is the exact failure the downstream
// allow-list exists to prevent, and the fuzzer found it.
//
// So a `<<WORD` only RECORDS a pending delimiter and scanning continues along the line.
// The bodies are skipped at the newline, which is where the shell reads them too.

// heredocPending is a delimiter whose body has not been consumed yet. Several can be
// pending at once: `cat <<A <<B` is legal, and the bodies arrive in order.
type heredocPending struct {
	word     string
	tabStrip bool // <<- strips leading TABS from the terminator, never spaces
}

// parseHeredocDelimiter reads a `<<` / `<<-` / `<<'WORD'` operator at start.
//
// Returns next = the offset just past the delimiter token, so the caller keeps scanning
// the rest of the START line. ok=false means this is not a heredoc worth tracking —
// `<<<` is a here-string with no body, and `<<` with no word is unparseable.
func parseHeredocDelimiter(cmd string, start int) (h heredocPending, next int, ok bool) {
	i := start + 2 // past "<<"
	if i < len(cmd) && cmd[i] == '<' {
		return heredocPending{}, start + 3, false // here-string: no body to skip
	}
	if i < len(cmd) && cmd[i] == '-' {
		h.tabStrip = true
		i++
	}
	for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
		i++
	}
	var quote byte
	if i < len(cmd) && (cmd[i] == '\'' || cmd[i] == '"') {
		quote = cmd[i]
		i++
	}
	wordStart := i
	for i < len(cmd) {
		c := cmd[i]
		if quote != 0 {
			if c == quote {
				break
			}
		} else if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '|' || c == '&' || c == '>' || c == '<' {
			break
		} else if c == '\'' || c == '"' || c == '\\' {
			// An UNQUOTED word must not contain quoting characters. Stopping here makes
			// the boundary check below reject the token, which is what we want: in
			// `<<F'\nF'\ngo test` bash's quote spans the newline, so the delimiter is
			// `F\nF`, nothing matches it, and `go test` is BODY text. Reading the word as
			// `F'` made line 2 look like the terminator and wrapped body text as a
			// command. Same failure class as the concatenated form, one level deeper —
			// the earlier fix only inspected the byte AFTER the word.
			break
		}
		i++
	}
	h.word = cmd[wordStart:i]
	if h.word == "" {
		return heredocPending{}, 0, false
	}
	if quote != 0 {
		if i >= len(cmd) {
			return heredocPending{}, 0, false // unterminated quote around the delimiter
		}
		i++ // past the closing quote
	}
	// The delimiter must be ONE simple word. Bash builds it from CONCATENATED parts and
	// removes quotes, so `<<'F''<<0'` is the single delimiter `F<<0` — a following line
	// `F` does not terminate it and the body runs to end of input. Reading only the first
	// quoted chunk made sctx see delimiter `F`, end the body early, and wrap the body text
	// `go test` as a command. Rather than reproduce bash's word assembly for a form nobody
	// writes, require the word to END at a real boundary and decline otherwise.
	if i < len(cmd) {
		switch cmd[i] {
		case ' ', '\t', '\n', ';', '|', '&', '>', '<', ')':
		default:
			return heredocPending{}, 0, false
		}
	}
	return h, i, true
}

// skipHeredocBodies consumes the bodies of every pending heredoc, starting at from
// (the first byte after the newline that ended the start line).
//
// Returns the offset of the newline that ends the LAST terminator line, so the caller
// still sees that newline and can emit a segment separator — consuming it merged the
// heredoc command with whatever followed. Returns len(cmd) when the terminator is the
// final line, and ok=false for an unterminated heredoc, which is unparseable and must
// make the whole command decline.
func skipHeredocBodies(cmd string, from int, pending []heredocPending) (int, bool) {
	pos := from
	for _, h := range pending {
		found := false
		for pos <= len(cmd) {
			lineEnd := strings.IndexByte(cmd[pos:], '\n')
			var line string
			if lineEnd < 0 {
				line = cmd[pos:]
			} else {
				line = cmd[pos : pos+lineEnd]
			}
			terminator := line == h.word
			if !terminator && h.tabStrip {
				// `<<-` strips leading TABS only. Matching spaces too would terminate on
				// a line bash treats as body, truncating the heredoc and turning its
				// remaining lines into commands.
				terminator = strings.TrimLeft(line, "\t") == h.word
			}
			if terminator {
				found = true
				if lineEnd < 0 {
					pos = len(cmd)
				} else {
					pos += lineEnd // ON the newline, not past it
				}
				break
			}
			if lineEnd < 0 {
				return 0, false // ran out of input without a terminator
			}
			pos += lineEnd + 1
		}
		if !found {
			return 0, false
		}
		if pos < len(cmd) && cmd[pos] == '\n' {
			pos++ // step over this terminator's newline before the next body
		}
	}
	if pos > 0 && pos <= len(cmd) {
		// Hand back the newline position for the caller's separator.
		if pos == len(cmd) {
			return len(cmd), true
		}
		return pos - 1, true
	}
	return pos, true
}
