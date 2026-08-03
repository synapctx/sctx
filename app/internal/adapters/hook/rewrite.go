// Package hook implements the rewrite logic behind `sctx hook claude`, the
// Claude Code PreToolUse Bash hook that transparently prefixes plain
// commands with "sctx " so agents get token-minimal output without changing
// their habits.
package hook

import "strings"

// pipeSafeDownstream lists programs that are safe to leave downstream of a
// wrapped segment in a pipeline: they only ever narrow/truncate the stream
// (never reorder, count, or transform lines in a way that a compressed
// rendering would corrupt), so `sctx <cmd> | <one of these>` still shows the
// agent a faithful (if truncated) view. Deliberately excluded: grep, rg,
// sed, awk, cut, sort, uniq (all could silently operate on compressed
// output and produce a wrong answer), wc (counting compressed lines is
// silently wrong), jq, xargs, tee (unclear semantics over reformatted text).
var pipeSafeDownstream = map[string]bool{
	"head": true, "tail": true, "cat": true, "less": true, "more": true,
}

// noiseBuiltins are shell built-ins/no-ops that never represent a coverage
// gap on their own — gapSegment skips over them rather than treating them
// as "the" program of a compound command (e.g. `cd sub && npm test` should
// report a gap for npm test, not decline because cd isn't in any table).
var noiseBuiltins = map[string]bool{
	"cd": true, "pushd": true, "popd": true,
	"export": true, "set": true, "unset": true,
	"source": true, ".": true,
	"echo": true, "printf": true,
	"true": true, "false": true, "exit": true,
	// Shell KEYWORDS, not programs. The live meter recorded `for` 19 times —
	// `for f in ...; do` is a loop, and a formatter for it is not a thing.
	// Anything ranked here competes with real gaps for attention.
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"case": true, "esac": true, "function": true, "select": true, "time": true,
	"local": true, "declare": true, "readonly": true, "eval": true, "shift": true,
	"return": true, "break": true, "continue": true, ":": true,
}

// alreadyWrapped reports whether a segment head IS sctx, however it was
// invoked. Compared by BASENAME because the live meter recorded `./bin/sctx`
// as a coverage gap: a developer running a locally-built binary was reported
// as a command sctx cannot handle, which is the opposite of the truth.
func alreadyWrapped(head string) bool {
	if i := strings.LastIndexAny(head, `/\`); i >= 0 {
		head = head[i+1:]
	}
	return head == "sctx" || head == "sctx.exe"
}

// subcommandTable lists, per program, the first-argument subcommands that
// should be rewritten. A nil slice means "always rewrite" regardless of
// subcommand.
var subcommandTable = map[string][]string{
	"go":            {"test", "build", "vet", "mod", "list", "run", "generate", "get"},
	"git":           {"status", "log", "diff", "show", "add", "commit", "push", "pull", "fetch", "branch", "stash", "blame", "reflog", "tag", "remote", "shortlog", "ls-files", "worktree"},
	"grep":          nil,
	"rg":            nil,
	"ls":            nil,
	"find":          nil,
	"tree":          nil,
	"docker":        {"ps", "images", "logs", "compose", "build", "inspect", "stats", "network", "volume", "container", "pull", "push", "history", "top"},
	"kubectl":       {"get", "describe", "logs", "top", "events", "rollout", "api-resources", "apply", "create", "delete", "patch", "scale", "label", "annotate"},
	"gh":            {"pr", "issue", "run", "repo", "api", "release"},
	"golangci-lint": {"run"},
	"make":          nil,
	"ps":            nil,
	"diff":          nil,
	"cat":           nil,
	"head":          nil,
	"tail":          nil,
	"pytest":        nil,
	"mypy":          nil,
	"ruff":          {"check", "format"},
	"brew":          {"install", "upgrade"},
	"pip":           {"install", "list", "show", "freeze", "download", "uninstall"},
	"pip3":          {"install", "list", "show", "freeze", "download", "uninstall"},
	"mongosh":       nil,
	"du":            nil,
	// rsync was the top real coverage gap (32 delegations in 7 days) once the meter was
	// decontaminated. Its first argument is a path, so it takes no subcommand.
	"rsync": nil,
	// ssh is wrapped so the ssh formatter can render the REMOTE command's output with that
	// command's own formatter — `ssh host 'docker ps'` gets the docker renderer. Its first
	// argument is a host, so no subcommand. Interactive sessions are not a concern here:
	// this table only governs the PreToolUse hook, which sees agent Bash calls, and the
	// formatter declines when no remote command is present.
	"ssh": nil,
	// jq and curl have no dedicated formatter; the run pipeline's JSON
	// content-sniffer (jsoncompact) compacts their JSON stdout automatically,
	// while non-JSON output degrades to verbatim. The hook row just ensures
	// bare invocations get wrapped so that compaction can happen.
	"jq":   nil,
	"curl": nil,
	"npm":  {"install", "i", "ci", "add", "update", "audit", "list", "ls", "outdated", "run", "test", "exec"},
	"pnpm": {"install", "i", "ci", "add", "update", "audit", "list", "ls", "outdated", "run", "test", "exec"},
	"yarn": {"install", "i", "ci", "add", "update", "audit", "list", "ls", "outdated", "run", "test", "exec"},
}

// reservedNames are sctx's own native subcommands: rewriting a bare
// invocation of one of these would be wrong (it's not a wrapped tool).
var reservedNames = map[string]bool{
	"gain":    true,
	"flush":   true,
	"doctor":  true,
	"version": true,
	"help":    true,
	"hook":    true,
}

// token is a whitespace-delimited word of a command string, plus its byte
// offset in the original (untrimmed) string.
type token struct {
	text   string
	offset int
}

// segment is one command in a `;`/`&&`/`||`/`|`-separated chain, as produced
// by splitSegments. text is the raw (untrimmed) slice of the original
// command string, so insertion offsets computed from it (via tokenize) stay
// valid without any re-scanning.
type segment struct {
	text     string // raw slice of cmd between separators, untrimmed
	start    int    // byte offset of text[0] within the original cmd
	pipeFrom bool   // true if the separator immediately before this segment was a single "|"
}

// separator kinds used internally by splitSegments to decide, when a
// separator is found, what pipeFrom should be set to on the next segment.
type sepKind int

const (
	sepNone sepKind = iota
	sepAnd
	sepOr
	sepPipe
	sepSemi
)

// splitSegments performs a single left-to-right, quote- and escape-aware
// scan of cmd, splitting it into the individual commands joined by `;`,
// `&&`, `||`, or `|`. It is deliberately conservative: encountering any
// construct it cannot reason about safely (subshells, command substitution,
// backticks, heredocs/newlines, backgrounding, unterminated quotes, a
// trailing backslash) makes the whole command "hard-unsafe" and splitSegments
// returns (nil, false). Callers must decline to rewrite anything in that
// case — this is the fail-open boundary that keeps sctx from ever inserting
// "sctx " inside a quoted string or otherwise misreading the command.
func splitSegments(cmd string) ([]segment, bool) {
	var segs []segment
	var inSingle, inDouble, escaped bool
	var pendingHeredocs []heredocPending
	// lineEnded records that the last separator was a LINE ENDING — a newline, or a
	// heredoc terminator consuming to end of input — rather than an operator. It decides
	// whether a segment boundary at end-of-string is a finished command or an unfinished
	// one: `go test\n` is complete, `go test &&` is not, and only the second should
	// decline.
	lineEnded := false
	segStart := 0
	pipeFrom := false

	emit := func(end, next int, sep sepKind) {
		segs = append(segs, segment{text: cmd[segStart:end], start: segStart, pipeFrom: pipeFrom})
		segStart = next
		pipeFrom = sep == sepPipe
	}

	n := len(cmd)
	for i := 0; i < n; i++ {
		c := cmd[i]

		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && !inSingle {
			escaped = true
			continue
		}
		if c == '`' {
			return nil, false
		}
		if c == '$' && i+1 < n && cmd[i+1] == '(' {
			return nil, false
		}
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if c == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			// A newline reached with a quote still open AND a heredoc pending is the one
			// case where bash's own answer is unusable. Verified against bash: in
			// `cat <<'F'"\n"\nF\necho AFTER` the open quote pulls the newline INTO the
			// delimiter word, so the delimiter is `F\n`, nothing ever matches it, and the
			// body swallows the rest of the input — `echo AFTER` is body text and never
			// runs. sctx read it as a command and wrapped it. Nobody writes this, and the
			// cost of guessing is inserting sctx into text, so decline.
			if c == '\n' && len(pendingHeredocs) > 0 {
				return nil, false
			}
			continue
		}
		if c == '\n' {
			if len(pendingHeredocs) > 0 {
				// The command ends at this newline; its heredoc bodies follow, and the
				// NEXT command starts after the last terminator.
				end, ok := skipHeredocBodies(cmd, i+1, pendingHeredocs)
				if !ok {
					return nil, false // unterminated heredoc: unparseable
				}
				pendingHeredocs = pendingHeredocs[:0]
				// `end` is the offset OF the newline closing the terminator line, or
				// len(cmd) when the terminator is the final line. Clamped because
				// end+1 past the end panicked on exactly that case.
				next := end + 1
				if next > n {
					next = n
				}
				emit(i, next, sepSemi)
				lineEnded = true
				i = next - 1
				continue
			}
			// A TOP-LEVEL newline separates commands exactly as ';' does.
			//
			// Placement after the quote guard is the whole correctness of this: a
			// newline INSIDE a quoted string is text. Checked before the guard — as it
			// was for one commit — `ssh host 'a\nls -l'` split inside the quotes and
			// sctx was inserted into the remote command, which failed on the remote
			// host with "sctx: command not found". That is precisely the corruption
			// this scanner exists to prevent, and CLAUDE.md names it as the one true
			// hazard.
			emit(i, i+1, sepSemi)
			lineEnded = true
			continue
		}
		if c == '<' && i+1 < n && cmd[i+1] == '<' {
			// Record the delimiter and KEEP SCANNING this line: the heredoc's start line
			// is ordinary shell, and its pipes and redirects decide whether the segment
			// may be wrapped at all. The body is skipped at the newline below, which is
			// where the shell reads it too.
			h, next, ok := parseHeredocDelimiter(cmd, i)
			if ok {
				pendingHeredocs = append(pendingHeredocs, h)
			} else if next == 0 {
				// `<<` with no delimiter, or an unterminated quote around it: not
				// something to reason about, so decline rather than guess.
				return nil, false
			}
			if next > i {
				i = next - 1
			}
			continue
		}
		if c == '(' || c == ')' {
			// Subshells and process substitution. Restored after an edit removed this guard
			// along with the heredoc branch: `(cd x && go test ./...)` and `go test <(cmd)`
			// both started being rewritten, which two existing tests caught immediately.
			return nil, false
		}
		if c == '&' {
			if i+1 < n && cmd[i+1] == '&' {
				emit(i, i+2, sepAnd)
				lineEnded = false
				i++
				continue
			}
			if i > 0 && cmd[i-1] == '>' {
				continue // fd-dup, e.g. 2>&1
			}
			if i+1 < n && cmd[i+1] == '>' {
				continue // &>file
			}
			return nil, false // backgrounding (also covers "|&")
		}
		if c == '|' {
			if i+1 < n && cmd[i+1] == '|' {
				emit(i, i+2, sepOr)
				lineEnded = false
				i++
				continue
			}
			emit(i, i+1, sepPipe)
			lineEnded = false
			continue
		}
		if c == ';' {
			emit(i, i+1, sepSemi)
			lineEnded = false
			continue
		}
		// '>' and '<' (redirects) are left in place; per-segment redirect
		// checks (hasDisallowedRedirect) decide later whether they matter.
	}

	if inSingle || inDouble {
		return nil, false
	}
	if escaped {
		return nil, false
	}
	if len(pendingHeredocs) > 0 {
		return nil, false // a heredoc was opened and its body never arrived
	}
	// Only append a trailing segment if there is something left, OR the last separator
	// did not end a line. rewrite declines on any segment with no program token, so a
	// separator that consumes to end-of-string leaves a PHANTOM segment that makes a
	// perfectly good command decline — that is how `git commit -F - <<EOF … EOF` came
	// back unwrapped, its heredoc terminator being the final line.
	//
	// The `lineEnded` half is what keeps that from over-applying: `go test\n` is a
	// finished command, but `go test &&` and `git status ;` are unfinished and must still
	// decline, which they do by producing exactly that empty segment. A genuinely empty
	// segment mid-command (`;;;`) is unaffected.
	if segStart < len(cmd) || !lineEnded {
		segs = append(segs, segment{text: cmd[segStart:], start: segStart, pipeFrom: pipeFrom})
	}
	return segs, true
}

// segmentHead returns the program token of a segment's text — the first
// token after skipping any leading env assignments (FOO=bar ...) — or "" if
// the segment has no tokens at all (e.g. an empty/whitespace-only segment
// produced by a doubled separator or a trailing one).
func segmentHead(text string) string {
	tokens := tokenize(text)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) {
		return ""
	}
	return tokens[idx].text
}

// hasDisallowedRedirect reports whether text contains any file-redirection
// token ('<' or '>' outside the exact "2>&1" fd-dup form). File redirects
// are out of scope for rewriting/gap-recording: writing a program's stdout
// to a file means the AI agent never sees sctx's compressed rendering, so
// wrapping it would be pointless at best and could sever the mechanism that
// keeps sctx's own stdout/stderr faithfully re-emitted.
func hasDisallowedRedirect(text string) bool {
	for _, tok := range tokenize(text) {
		// Only OUTPUT redirects disqualify a segment. The reason a redirect matters is
		// that it sends stdout somewhere sctx must not rewrite — a file gets the
		// compressed rendering instead of the real bytes, which corrupts it.
		//
		// INPUT redirects (`<file`, `<<EOF`, `<<<word`) do not touch stdout at all, and
		// sctx forwards stdin to the wrapped command (verified: `printf x | sctx cat`
		// echoes x). Rejecting them cost the savings on every heredoc command — of
		// which an agent writing commit messages and config files runs a great many —
		// for no safety gain.
		if strings.Contains(tok.text, ">") && tok.text != "2>&1" {
			return true
		}
	}
	return false
}

// Rewrite returns the sctx-prefixed form of cmd and true if a rewrite rule
// applies; otherwise it returns cmd unchanged and false. It never errors:
// any ambiguous or unsafe input simply isn't rewritten. Callers are
// responsible for honoring the SCT__REWRITE_DISABLED kill switch before
// calling Rewrite.
func Rewrite(cmd string) (string, bool) {
	return rewrite(cmd)
}

// rewrite is the unexported implementation, kept separate so tests can
// exercise it directly without any env-var plumbing.
//
// It first splits cmd into `;`/`&&`/`||`/`|`-separated segments (declining
// on anything hard-unsafe — subshells, substitution, backgrounding,
// newlines, unterminated quotes: see splitSegments). If every segment is
// already sctx-wrapped-clean, it scans left to right for the first
// wrappable pipeline head (see wrappable) whose program matchSegment
// recognizes, and inserts an "sctx " prefix before EVERY such segment — leaving
// every other byte of cmd untouched. `cd sub && go test ./...` and
// `go test ./... 2>&1 | tail -50` both rewrite; anything ambiguous declines.
func rewrite(cmd string) (string, bool) {
	segs, ok := splitSegments(cmd)
	if !ok || len(segs) == 0 {
		return cmd, false
	}

	for _, seg := range segs {
		h := segmentHead(seg.text)
		if h == "" {
			return cmd, false
		}
		if alreadyWrapped(h) {
			return cmd, false
		}
	}

	// EVERY eligible segment is wrapped, not just the first.
	//
	// Wrapping one meant `ls && go build ./...` compressed the `ls` and left the
	// build — the output-heavy half — untouched, because the cheap command happened
	// to come first. Each segment is an independent command, so wrapping each is
	// exactly equivalent to wrapping it alone, and it also makes telemetry attribute
	// savings to the right program instead of only to whichever ran first.
	//
	// Insertions are applied back-to-front so that each position, computed against
	// the ORIGINAL string, stays valid as earlier text is left untouched.
	var positions []int
	for i, seg := range segs {
		if !wrappable(segs, i) {
			continue
		}
		off, ok := matchSegment(seg.text)
		if !ok {
			continue
		}
		positions = append(positions, seg.start+off)
	}
	if len(positions) == 0 {
		return cmd, false
	}
	out := cmd
	for i := len(positions) - 1; i >= 0; i-- {
		pos := positions[i]
		out = out[:pos] + "sctx " + out[pos:]
	}
	return out, true
}

// wrappable reports whether segment i in segs is safe to prefix with
// "sctx ": it must itself be a pipeline head (not already receiving piped
// input), free of file redirects, and every segment immediately downstream
// of it via a single "|" must be on pipeSafeDownstream and also free of file
// redirects. This is what lets `go test ./... 2>&1 | tail -50` rewrite
// while `go test ./... | grep FAIL` correctly declines (grep could operate
// incorrectly over sctx's compressed rendering).
func wrappable(segs []segment, i int) bool {
	if segs[i].pipeFrom {
		return false
	}
	if hasDisallowedRedirect(segs[i].text) {
		return false
	}
	for j := i + 1; j < len(segs) && segs[j].pipeFrom; j++ {
		if !pipeSafeDownstream[segmentHead(segs[j].text)] {
			return false
		}
		if hasDisallowedRedirect(segs[j].text) {
			return false
		}
	}
	return true
}

// matchSegment checks whether text's program (after skipping leading env
// assignments) is one sctx knows how to rewrite, per subcommandTable, and if
// so returns the byte offset of that program token within text. It does not
// itself guard against sctx already having wrapped the command, or
// against reserved native subcommand names appearing bare — callers that
// need those guards apply them (typically via segmentHead) before calling
// matchSegment.
func matchSegment(text string) (int, bool) {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return 0, false
	}

	// Skip leading env assignments (FOO=bar cmd ...) when identifying the
	// program, but keep the full text intact for prefixing.
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) {
		return 0, false
	}
	program := tokens[idx].text

	if reservedNames[program] {
		return 0, false
	}

	subs, known := subcommandTable[program]
	if !known {
		return 0, false
	}
	if len(subs) > 0 {
		if idx+1 >= len(tokens) || !contains(subs, tokens[idx+1].text) {
			return 0, false
		}
	}

	return tokens[idx].offset, true
}

func gapSegment(cmd string) (string, bool) {
	segs, ok := splitSegments(cmd)
	if !ok {
		return "", false
	}

	for _, seg := range segs {
		if h := segmentHead(seg.text); alreadyWrapped(h) {
			return "", false
		}
	}

	for i, seg := range segs {
		if seg.pipeFrom {
			continue
		}
		h := segmentHead(seg.text)
		if h == "" {
			return "", false
		}
		if noiseBuiltins[h] {
			continue
		}
		if reservedNames[h] {
			return "", false
		}
		if !wrappable(segs, i) {
			return "", false
		}
		if _, covered := matchSegment(seg.text); covered {
			return "", false
		}
		return seg.text, true
	}

	return "", false
}

// tokenize splits s on runs of spaces/tabs, recording each token's byte
// offset in s.
func tokenize(s string) []token {
	var tokens []token
	i, n := 0, len(s)
	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		// Whitespace only separates tokens when it is UNQUOTED. Splitting naively made
		// `A=" go test "` look like the assignment `A="` followed by the program `go`,
		// and sctx was inserted at offset 4 — inside the string. Found by the fuzzer
		// once its invariant checked WHERE the insertion landed rather than only that
		// the command could be reassembled.
		var inSingle, inDouble, escaped bool
	scan:
		for i < n {
			c := s[i]
			switch {
			case escaped:
				escaped = false
			case c == '\\' && !inSingle:
				escaped = true
			case c == '\'' && !inDouble:
				inSingle = !inSingle
			case c == '"' && !inSingle:
				inDouble = !inDouble
			case (c == ' ' || c == '\t') && !inSingle && !inDouble:
				break scan
			}
			i++
		}
		tokens = append(tokens, token{text: s[start:i], offset: start})
	}
	return tokens
}

// isAssignment reports whether field looks like a POSIX env assignment
// (NAME=value), used to skip leading `FOO=bar` prefixes.
func isAssignment(field string) bool {
	eq := strings.Index(field, "=")
	if eq <= 0 {
		return false
	}
	name := field[:eq]
	for i, r := range name {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			continue
		case i > 0 && r >= '0' && r <= '9':
			continue
		default:
			return false
		}
	}
	return true
}

func contains(items []string, s string) bool {
	for _, it := range items {
		if it == s {
			return true
		}
	}
	return false
}
