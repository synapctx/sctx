package hook

import (
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/platform/launcher"
)

// inlineOptOutVar is the per-segment kill switch: `SCT__REWRITE_DISABLED=1 cmd`
// is never rewritten. It is the same name as the process-wide switch
// rewriteForAgent honours, so one spelling covers both scopes.
const inlineOptOutVar = "SCT__REWRITE_DISABLED"

// optOutAssignment reports whether a token is an inline opt-out assignment.
func optOutAssignment(tok string) bool {
	name, value, ok := strings.Cut(tok, "=")
	if !ok || name != inlineOptOutVar {
		return false
	}
	return rewriteDisabledValue(shellTokenValue(value))
}

// rewriteDisabledValue is the ONE spelling of "switched off" for
// SCT__REWRITE_DISABLED, inline or process-wide: 1/true/yes/on, any case.
func rewriteDisabledValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// leadingOptOut reports whether the segment starts with an inline opt-out.
func leadingOptOut(tokens []token) bool {
	for _, t := range tokens {
		if !isAssignment(t.text) {
			return false
		}
		if optOutAssignment(t.text) {
			return true
		}
	}
	return false
}

var (
	durationRe   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)
	niceNumberRe = regexp.MustCompile(`^-?[0-9]+$`)
	niceShortRe  = regexp.MustCompile(`^-[0-9]+$`)
	stdbufRe     = regexp.MustCompile(`^-[oie]([LlB0]|[0-9]+[KMG]?)$`)
	stdbufLongRe = regexp.MustCompile(`^--(input|output|error)=([LlB0]|[0-9]+[KMG]?)$`)
	envNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// skipWrapper recognises a process wrapper at tokens[i] and returns the index
// of the first token after the wrapper and its own arguments, i.e. the command
// it launches. ok is false when tokens[i] is not a wrapper, when a wrapper flag
// is not one this parser understands, or when no command follows.
//
// A wrapper still has to govern the process, so sctx is inserted AFTER it
// (`timeout 60 sctx go test`), never before. Only wrapper flags whose arity is
// known are accepted: an unknown flag could consume the next token, and
// guessing wrong would put sctx inside the wrapper's own arguments.
//
// sudo is deliberately not a wrapper here (sctx is not on root's PATH) and nor
// is xargs (its stdin is the argument stream, not the command's input).
func skipWrapper(tokens []token, i int) (next int, optOut, ok bool) {
	if strings.ContainsAny(tokens[i].text, "/\\") {
		return 0, false, false
	}
	j := i + 1
	switch tokens[i].text {
	case "nohup":
		// takes no options of its own that we accept
	case "time":
		if j < len(tokens) && tokens[j].text == "-p" {
			j++
		}
	case "nice":
		if j < len(tokens) {
			switch t := tokens[j].text; {
			case t == "-n":
				if j+1 >= len(tokens) || !niceNumberRe.MatchString(tokens[j+1].text) {
					return 0, false, false
				}
				j += 2
			case niceShortRe.MatchString(t), strings.HasPrefix(t, "--adjustment=") && niceNumberRe.MatchString(t[len("--adjustment="):]):
				j++
			}
		}
	case "stdbuf":
		for j < len(tokens) && strings.HasPrefix(tokens[j].text, "-") {
			if !stdbufRe.MatchString(tokens[j].text) && !stdbufLongRe.MatchString(tokens[j].text) {
				return 0, false, false
			}
			j++
		}
	case "timeout":
		// Only the default signal (or TERM/INT) is peeled. With `-s KILL`, or
		// `-k` (a KILL after a grace period), the signal can hit sctx itself
		// while the child ignores TERM: the child is orphaned and the output
		// sctx was buffering is lost. Declining keeps timeout's own behaviour.
		var done bool
		for j < len(tokens) && !done {
			t := tokens[j].text
			switch {
			case t == "--foreground", t == "--preserve-status", t == "-v", t == "--verbose":
				j++
			case t == "-s":
				if j+1 >= len(tokens) || !timeoutSignalOK(tokens[j+1].text) {
					return 0, false, false
				}
				j += 2
			case strings.HasPrefix(t, "--signal="):
				if !timeoutSignalOK(t[len("--signal="):]) {
					return 0, false, false
				}
				j++
			case strings.HasPrefix(t, "-"):
				// includes -k / --kill-after and every unknown flag
				return 0, false, false
			default:
				done = true
			}
		}
		if j >= len(tokens) || !durationRe.MatchString(tokens[j].text) {
			return 0, false, false
		}
		j++
	case "env":
		// `env -i`, `-u PATH` and any PATH= assignment change the lookup path
		// the rewritten command runs under: `sctx` is then not found (exit
		// 127). Declined rather than guessed.
		for j < len(tokens) {
			t := tokens[j].text
			switch {
			case isAssignment(t):
				if strings.HasPrefix(t, "PATH=") {
					return 0, false, false
				}
				if optOutAssignment(t) {
					optOut = true
				}
				j++
				continue
			case t == "-u":
				if j+1 >= len(tokens) || !envNameRe.MatchString(tokens[j+1].text) || tokens[j+1].text == "PATH" {
					return 0, false, false
				}
				j += 2
				continue
			case strings.HasPrefix(t, "--unset="):
				if v := t[len("--unset="):]; !envNameRe.MatchString(v) || v == "PATH" {
					return 0, false, false
				}
				j++
				continue
			case strings.HasPrefix(t, "-"):
				// includes -i / --ignore-environment
				return 0, false, false
			}
			break
		}
	default:
		return 0, false, false
	}
	if j >= len(tokens) {
		return 0, false, false
	}
	return j, optOut, true
}

// timeoutSignalOK reports whether v names the default, TERM or INT signal.
func timeoutSignalOK(v string) bool {
	switch strings.ToUpper(v) {
	case "TERM", "SIGTERM", "15", "INT", "SIGINT", "2":
		return true
	}
	return false
}

// safeProgramPath reports whether a program token is a plain path whose
// basename can be looked up: no quoting, expansion or globbing the hook could
// misread. `~` and `$HOME` prefixes are fine for the shell but are not
// accepted here, so the answer never depends on expansion.
func safeProgramPath(p string) bool {
	if !strings.Contains(p, "/") || strings.HasSuffix(p, "/") {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return true
}

// lookupName maps a program token to its subcommandTable key: a literal row
// (`./gradlew`) wins, otherwise a plain path is reduced to its basename so
// `/usr/local/bin/make` and `/opt/homebrew/bin/rg` find their rows. The
// rewritten command keeps the path as written.
func lookupName(program string) string {
	if _, ok := subcommandTable[program]; ok {
		return program
	}
	if safeProgramPath(program) {
		return program[strings.LastIndexByte(program, '/')+1:]
	}
	return program
}

// headOf locates the program of a segment's tokens.
//
//	insert — index of the program token, after any wrapper: sctx goes in front
//	         of it, and its name selects the table row
//	inner  — index of the program a launcher runs, or -1; innerName is its
//	         name with any @version removed
//
// ok is false for anything ambiguous, and when the segment opts out.
func headOf(tokens []token) (insert, inner int, innerName string, ok bool) {
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if leadingOptOut(tokens) || idx >= len(tokens) {
		return 0, -1, "", false
	}
	for depth := 0; depth < 4; depth++ {
		next, optOut, wrapped := skipWrapper(tokens, idx)
		if !wrapped {
			break
		}
		if optOut {
			return 0, -1, "", false
		}
		idx = next
	}
	inner = -1
	argv := make([]string, len(tokens)-idx)
	for k, t := range tokens[idx:] {
		argv[k] = t.text
	}
	if at, name, found := launcher.Peel(argv); found {
		inner, innerName = idx+at, name
	}
	return idx, inner, innerName, true
}
