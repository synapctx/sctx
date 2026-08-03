package hook

import (
	"strings"
	"unicode"
)

// Roadmap item 0b: after a developer greps for an identifier, tell them about
// the call sites their search could not have seen.
//
// It rides on the SAME PostToolUse hook as memory surfacing rather than the grep
// formatter, and that is the significant decision. The formatter renders output
// inside the command, so a lookup there adds latency to every grep the developer
// runs — on a tool sold for making commands cheaper. PostToolUse runs after the
// command has already returned its answer, so the network call is pure upside:
// if it is slow or fails, the developer still has exactly what they asked for.
//
// The precision rules live here, before any network call, because the cheapest
// nudge is the one we decide not to make.
//
// NOT YET ENABLED. `sctx setup` registers this hook for Edit|Write only — see
// agentsetup/hooks.go. Both sides of the path work; what does not work yet is
// cheap resolution of a grep pattern to a symbol path, which today costs a
// ~1.1s semantic retrieval. Everything here is exercised by tests and ready for
// the day the engine can answer that by term lookup.

// grepSymbol extracts the identifier a command searched for, or "" when the
// command is not a symbol search we can speak to.
//
// Deliberately conservative. A false positive costs a pointless round trip and,
// worse, an irrelevant line in an agent's context; a false negative costs
// nothing but a missed opportunity. When in doubt, say nothing.
func grepSymbol(cmd string) string {
	fields := splitArgs(cmd)
	if len(fields) == 0 {
		return ""
	}
	prog := fields[0]
	if i := strings.LastIndexAny(prog, `/\`); i >= 0 {
		prog = prog[i+1:]
	}
	// `sctx grep …` is the rewritten form the hook itself produced, so it has to
	// be seen through — otherwise this never fires in the configuration we ship.
	if prog == "sctx" {
		if len(fields) < 2 {
			return ""
		}
		fields = fields[1:]
		prog = fields[0]
	}
	switch prog {
	case "grep", "rg", "ag", "ack":
	default:
		return ""
	}

	var candidate string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		if candidate == "" {
			candidate = strings.Trim(f, `"'`)
			continue
		}
		// A second bare argument is the path. Fine — a symbol search usually has
		// one. More than that and we are guessing about which is the pattern.
	}
	if !isPlainIdentifier(candidate) {
		return ""
	}
	return candidate
}

// isPlainIdentifier is the precision gate, and every clause pays for itself.
//
// A regex, a phrase or a fragment is not a symbol, and asking the graph about it
// wastes a round trip to produce nothing. Short names are excluded because they
// match everywhere and the answer is never specific enough to act on.
func isPlainIdentifier(s string) bool {
	// Long enough to be a real name. "id", "err" and "ok" appear in every
	// repository and an "also used in 40 places" note about them is pure noise.
	if len(s) < 5 || len(s) > 120 {
		return false
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '_':
		case r == '.':
			// Allowed: a qualified name like pkg.Symbol is MORE specific, not
			// less, and is exactly the shape find_references indexes.
		default:
			// Anything else — a space, a regex metacharacter, a slash — means
			// this was a text search, not a symbol lookup.
			return false
		}
	}
	// A pure number is a magic-value search, not a symbol.
	return strings.IndexFunc(s, unicode.IsLetter) >= 0
}

// splitArgs is a whitespace split that respects quotes, so `grep "func Foo"` is
// one argument and is then correctly rejected as a phrase rather than being read
// as the identifier `func`.
func splitArgs(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				// Keep an empty quoted argument distinguishable from no argument.
				if cur.Len() == 0 {
					out = append(out, "")
				}
				continue
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}
