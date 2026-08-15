// Package hook implements the rewrite logic behind `sctx hook claude`, the
// Claude Code PreToolUse Bash hook that transparently prefixes plain
// commands with "sctx " so agents get token-minimal output without changing
// their habits.
package hook

import (
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/projectfilter"
	"github.com/synapctx/sctx/internal/platform/dockerargv"
	"github.com/synapctx/sctx/internal/platform/ghargv"
	"github.com/synapctx/sctx/internal/platform/gitargv"
	"github.com/synapctx/sctx/internal/platform/kubectlargv"
	"github.com/synapctx/sctx/internal/platform/sshargv"
)

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

// unformattable are programs for which A FORMATTER IS NOT A COHERENT ARTEFACT, so
// counting them as coverage gaps can only ever misdirect the roadmap. Two
// categories, one rule each — this is not a list of things we have not got round to.
//
// WHY THIS EXISTS. The gap meter asked SEVEN times, in escalating counts
// (25 → 27 → 34 → 45 → 48 → 64), for a `python3` formatter. There cannot be one:
// python3's output is whatever the script prints. The shape is determined by USER
// CODE, not by the program, so there is nothing to parse. Measured 2026-08-04: of
// the recorded gaps, `sed` (207), `python3` (98), `deploy-bundle.sh` (25), `wc`
// (23), `cp` (11) and `sleep` (10) — 374 events — were all of this kind, and they
// were burying `terraform plan` (13), which is a real and valuable target because
// plan output is enormous and highly structured.
//
// The meter cannot rank by VALUE and structurally never will: `spoolCoverageGap`
// runs in the PreToolUse hook, BEFORE the command executes, and when the hook
// declines to wrap, the output never passes through sctx at all. So every gap
// event carries rawTokens=0 — not a bug, a consequence. Invocation count is the
// only signal available, which makes excluding the incoherent entries the only way
// the ranking means anything.
//
// This is the same discipline as noiseBuiltins above and the shell-keyword
// entries in it: a detector that floods with non-actionable hits gets ignored, and
// an ignored detector is worse than none because it reads as coverage.
var unformattable = map[string]bool{
	// 1. GENERAL-PURPOSE INTERPRETERS. Output is defined by the program they run,
	// not by them. `python3 -m pytest` is worth wrapping, but that is a formatter
	// for PYTEST — which already exists and has its own row.
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true,
	"sh": true, "bash": true, "zsh": true, "osascript": true,
	// awk and sed are stream editors: their output is the transformed stream, i.e.
	// arbitrary text chosen by the expression. sed alone was the single largest
	// entry in the gap list.
	"awk": true, "sed": true,

	// 2. NO MEANINGFUL OUTPUT TO COMPRESS. These are silent on success or emit a
	// single scalar, so a formatter has nothing to do. Excluded on that basis, not
	// on frequency.
	"cp": true, "mv": true, "rm": true, "mkdir": true, "rmdir": true,
	"touch": true, "chmod": true, "chown": true, "ln": true, "mktemp": true,
	"sleep": true, "kill": true, "wait": true, "wc": true,
	"basename": true, "dirname": true, "which": true, "test": true,
}

// isScript reports whether a program is a SCRIPT rather than a program we could
// ship a formatter for. Same rule as the interpreters above — the output shape is
// user code's, not ours — and it is why `deploy-bundle.sh` was being reported as a
// gap 25 times. Matched on the basename so `./scripts/x.sh` is caught.
func isScript(head string) bool {
	if i := strings.LastIndexAny(head, `/\`); i >= 0 {
		head = head[i+1:]
	}
	for _, ext := range []string{".sh", ".bash", ".zsh", ".py", ".pl", ".rb", ".js", ".ts"} {
		if strings.HasSuffix(head, ext) {
			return true
		}
	}
	return false
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
	"go": {"test", "build", "vet", "mod", "list", "run", "generate", "get"},
	// Git's finite porcelain surface is intentionally not enumerated. The
	// shared argv parser finds the command after global flags and rejects the
	// interactive/streaming protocol verbs; unknown finite verbs then receive
	// relaxed/generic/verbatim fallback without being mislabeled structured.
	"git":           nil,
	"grep":          nil,
	"rg":            nil,
	"ls":            nil,
	"find":          nil,
	"tree":          nil,
	"docker":        {"ps", "images", "image", "logs", "compose", "build", "inspect", "stats", "network", "volume", "container", "pull", "push", "history", "top", "exec"},
	"kubectl":       {"get", "describe", "logs", "top", "events", "rollout", "api-resources", "apply", "create", "delete", "patch", "scale", "label", "annotate", "exec"},
	"gh":            {"pr", "issue", "run", "repo", "api", "release", "search", "workflow", "cache", "gist", "project"},
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
	// argument is a host, so no subcommand. The shared ssh argv grammar below admits only a
	// finite, non-TTY remote command; an interactive ssh session must never be buffered.
	"ssh": nil,
	// jq and curl have no dedicated formatter; the run pipeline's JSON
	// content-sniffer (jsoncompact) compacts their JSON stdout automatically,
	// while non-JSON output degrades to verbatim. The hook row just ensures
	// bare invocations get wrapped so that compaction can happen.
	"jq":   nil,
	"curl": nil,
	// sqlite3's native default/list/csv output is arbitrary query data and must
	// remain verbatim. Wrapping still reaches the generic JSON/repeat detector
	// for caller-requested -json output and repetitive result streams.
	"sqlite3": nil,
	"npm":     {"install", "i", "ci", "add", "update", "audit", "list", "ls", "outdated", "run", "test", "exec"},
	"pnpm":    {"install", "i", "ci", "add", "update", "audit", "list", "ls", "outdated", "run", "test", "exec"},
	"yarn":    {"install", "i", "ci", "add", "update", "audit", "list", "ls", "outdated", "run", "test", "exec"},

	// ── Covered by the GENERIC formatter only, deliberately ──────────────────
	//
	// None of the rows below has a dedicated formatter, and that is the point.
	// Every one of them either speaks JSON on request (the cloud CLIs default to
	// it or take `--output json` / `--format json`) or prints long runs of
	// repeated progress and status lines — the two shapes the generic formatter
	// detects. It compacts what parses and collapses what repeats, and declines
	// otherwise, so the worst case is exactly the verbatim output these commands
	// produce today.
	//
	// WHY NOT SUBCOMMANDS. `aws` and `gcloud` alone have thousands of verbs
	// between them; enumerating those is a treadmill no one can stay on, and the
	// value is in the output SHAPE rather than the verb. A nil slice wraps every
	// invocation and lets detection decide — the same arrangement `curl` and `jq`
	// have always used.
	//
	// WHY WITHOUT MEASURED DEMAND, when this file's other rows were all earned by
	// the coverage meter: the telemetry corpus is a single developer's macOS
	// laptop, which has never run a cloud CLI. That is evidence about one estate,
	// not about the world, and treating "absent from my laptop" as "absent from
	// the market" would build a tool for an audience of one. These rows are an
	// explicit bet on users we cannot see, priced at roughly nothing because they
	// add no formatter to maintain and cannot render worse than verbatim.
	"aws":        nil,
	"gcloud":     nil,
	"az":         nil,
	"terraform":  nil,
	"tofu":       nil,
	"pulumi":     nil,
	"helm":       nil,
	"cargo":      {"test", "build", "check", "clippy", "run", "fmt", "tree", "add", "update"},
	"dotnet":     {"build", "test", "restore", "run", "publish", "list"},
	"mvn":        nil,
	"gradle":     nil,
	"./gradlew":  nil,
	"composer":   {"install", "update", "require", "show", "outdated", "validate"},
	"bundle":     {"install", "update", "exec", "list", "outdated"},
	"uv":         {"pip", "run", "sync", "lock", "add", "tree"},
	"poetry":     {"install", "update", "add", "show", "lock", "run"},
	"tsc":        nil,
	"eslint":     nil,
	"systemctl":  {"status", "list-units", "list-unit-files", "show", "is-active", "cat"},
	"journalctl": nil,
	"df":         nil,
	"terragrunt": nil,
	// Keyed by the literal token, because matchSegment compares argv[0] as
	// written rather than by basename — `./gradlew` is how projects invoke it.
	"gradlew": nil,
}

// argvOneIsOperation names programs wrapped for EVERY invocation (a nil row
// above) whose first argument is nonetheless an OPERATION, not a path.
//
// It exists because `nil` in subcommandTable was carrying two meanings at once.
// For `ls`, `find`, `cat` and `make`, nil means "argv[1] is a path or target", and
// joining it into a telemetry key would ship a customer's directory names to the
// platform — the leak TestSubcommandTableIsCoveredByProgkey exists to prevent.
// For `aws`, `gcloud` and `terraform`, nil means something different: wrap every
// invocation because the VERB surface is too large to enumerate, while argv[1] is
// still a bounded vocabulary the tool defines (`s3`, `compute`, `plan`) and is
// exactly the dimension the coverage meter needs to distinguish
// `terraform plan` from `terraform apply`.
//
// Listing them explicitly keeps the privacy guarantee intact — a program only
// gets argv[1] recorded if it appears here or declares subcommands — while
// letting the rewrite table stay off the enumeration treadmill.
// Listed ONLY where argv[1] is genuinely from the tool's own vocabulary.
// Deliberately absent: `tsc` and `eslint` (argv[1] is a path or a flag),
// `journalctl` and `df` (flags), `mvn`/`gradle` (a goal is close, but a bare
// `mvn -f pom.xml` puts a path there and the cost of being wrong is a leak).
var argvOneIsOperation = map[string]bool{
	"git": true,
	"aws": true, "gcloud": true, "az": true,
	"terraform": true, "tofu": true, "terragrunt": true, "pulumi": true, "helm": true,
}

// followFlags names, per program, the flags that make it stream until killed.
//
// A WRAPPED FOLLOW IS WORSE THAN NO WRAPPING AT ALL, which is why this exists.
// sctx reads stdout to EOF before formatting, so `kubectl logs -f`, `tail -f` or
// `journalctl -f` would buffer forever and the agent's timeout would fire having
// been shown NOTHING — whereas unwrapped it would at least have seen the lines
// printed so far. The saving is irrelevant next to that.
//
// SCOPED TO THE SUBCOMMAND THAT ACTUALLY STREAMS, never blanket per program,
// because `-f` is among the most overloaded flags in unix. `docker build -f
// Dockerfile`, `helm install -f values.yaml`, `make -f Makefile` and `grep -f
// patterns` all name a FILE, and a program-wide rule would silently stop
// wrapping every one of them — losing real coverage to protect against a case
// that cannot arise.
var followFlags = []string{"-f", "-F", "--follow"}

// streamsForever reports whether this invocation follows output indefinitely.
// program is argv[0]; args are the tokens after it.
func streamsForever(program string, args []string) bool {
	switch program {
	case "tail", "journalctl":
		// No subcommand: the flag alone decides.
	case "kubectl":
		inv, ok := kubectlargv.Parse(append([]string{program}, args...))
		if !ok {
			return false
		}
		switch inv.Command {
		case "logs":
			return kubectlargv.HasFlag(inv.Args, "-f", "--follow")
		case "get", "events":
			return kubectlargv.HasFlag(inv.Args, "-w", "--watch", "--watch-only")
		case "exec":
			return kubectlargv.HasFlag(inv.Args, "-i", "--stdin", "-t", "--tty")
		case "attach", "edit", "port-forward", "proxy":
			return true
		case "debug", "run":
			return kubectlargv.HasFlag(inv.Args, "-i", "--stdin", "-t", "--tty", "--attach")
		default:
			return false
		}
	case "docker":
		inv, ok := dockerargv.Parse(append([]string{program}, args...))
		if !ok {
			return false
		}
		if inv.Command == "logs" {
			return dockerargv.HasFlag(inv.Args, "-f", "--follow")
		}
		if inv.Command == "stats" {
			return !dockerargv.HasFlag(inv.Args, "--no-stream")
		}
		if inv.Command == "attach" || inv.Command == "events" {
			return true
		}
		if inv.Command != "compose" {
			return false
		}
		nested, ok := dockerargv.ParseCompose(inv)
		if !ok {
			return false
		}
		switch nested.Command {
		case "logs":
			return dockerargv.HasFlag(nested.Args, "-f", "--follow")
		case "stats":
			return !dockerargv.HasFlag(nested.Args, "--no-stream")
		case "up":
			return !dockerargv.HasFlag(nested.Args, "-d", "--detach")
		case "attach", "events", "watch":
			return true
		default:
			return false
		}
	default:
		return false
	}
	for _, a := range args {
		for _, f := range followFlags {
			if a == f {
				return true
			}
		}
	}
	return false
}

func containsToken(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func kubectlExecSafe(args []string) bool {
	inner, ok := kubectlargv.ExecCommand(args)
	return ok && hookKnowsInner(inner)
}

func dockerInvocationSafe(argv []string) bool {
	inv, ok := dockerargv.Parse(argv)
	if !ok {
		return false
	}
	switch inv.Command {
	case "ps", "images", "logs", "build", "inspect", "stats", "pull", "push", "history", "top":
		return true
	case "network", "volume", "container", "image":
		return len(inv.Args) > 0 && inv.Args[0] == "ls"
	case "exec":
		inner, ok := dockerargv.ExecCommand("exec", inv.Args)
		return ok && hookKnowsInner(inner)
	case "compose":
		nested, ok := dockerargv.ParseCompose(inv)
		if !ok {
			return false
		}
		switch nested.Command {
		case "ps", "up", "build", "logs", "down":
			return true
		case "exec":
			inner, ok := dockerargv.ExecCommand("compose exec", nested.Args)
			return ok && hookKnowsInner(inner)
		default:
			return false
		}
	default:
		return false
	}
}

func hookKnowsInner(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	program := argv[0]
	if i := strings.LastIndexAny(program, `/\\`); i >= 0 {
		program = program[i+1:]
	}
	_, known := subcommandTable[program]
	return known && program != "sctx"
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
	return rewriteWithProject(cmd, "", nil)
}

func rewriteWithProject(cmd, root string, matchers []projectfilter.Matcher) (string, bool) {
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
			off, ok = matchProjectSegment(seg.text, root, matchers)
		}
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

func matchProjectSegment(text, root string, matchers []projectfilter.Matcher) (int, bool) {
	if root == "" || len(matchers) == 0 {
		return 0, false
	}
	tokens := tokenize(text)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) {
		return 0, false
	}
	program := shellTokenValue(tokens[idx].text)
	if reservedNames[filepathBase(program)] || alreadyWrapped(program) {
		return 0, false
	}
	argv := []string{program}
	for _, token := range tokens[idx+1:] {
		argv = append(argv, shellTokenValue(token.text))
	}
	for _, matcher := range matchers {
		if matcher.Matches(root, argv) {
			return tokens[idx].offset, true
		}
	}
	return 0, false
}

func filepathBase(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
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
	lookupProgram := program
	if i := strings.LastIndexAny(lookupProgram, `/\`); i >= 0 {
		base := lookupProgram[i+1:]
		if base == "git" || base == "gh" {
			lookupProgram = base
		}
	}

	if reservedNames[lookupProgram] {
		return 0, false
	}

	subs, known := subcommandTable[lookupProgram]
	if !known {
		return 0, false
	}
	if lookupProgram == "git" {
		argv := make([]string, 0, len(tokens)-idx)
		for _, token := range tokens[idx:] {
			argv = append(argv, token.text)
		}
		inv, ok := gitargv.Parse(argv)
		if !ok || !gitargv.SafeToBuffer(inv) {
			return 0, false
		}
	} else if lookupProgram == "gh" {
		argv := make([]string, 0, len(tokens)-idx)
		for _, token := range tokens[idx:] {
			argv = append(argv, token.text)
		}
		inv, ok := ghargv.Parse(argv)
		if !ok || !ghargv.SafeReadOnly(inv) {
			return 0, false
		}
	} else if lookupProgram == "ssh" {
		argv := make([]string, 0, len(tokens)-idx)
		for _, token := range tokens[idx:] {
			argv = append(argv, shellTokenValue(token.text))
		}
		inner, ok := sshargv.RemoteCommand(argv)
		if !ok || !hookKnowsInner(inner) {
			return 0, false
		}
	} else if len(subs) > 0 {
		switch lookupProgram {
		case "kubectl":
			argv := make([]string, 0, len(tokens)-idx)
			for _, token := range tokens[idx:] {
				argv = append(argv, token.text)
			}
			inv, ok := kubectlargv.Parse(argv)
			if !ok || !contains(subs, inv.Command) {
				return 0, false
			}
			if inv.Command == "exec" && !kubectlExecSafe(inv.Args) {
				return 0, false
			}
		case "docker":
			argv := make([]string, 0, len(tokens)-idx)
			for _, token := range tokens[idx:] {
				argv = append(argv, token.text)
			}
			if !dockerInvocationSafe(argv) {
				return 0, false
			}
		default:
			if idx+1 >= len(tokens) || !contains(subs, tokens[idx+1].text) {
				return 0, false
			}
		}
	}

	// A following command never reaches EOF, and sctx formats only after it does.
	// Wrapping one turns "the agent sees output until its timeout" into "the agent
	// sees nothing at all" — a strict regression that no saving could justify.
	args := make([]string, 0, len(tokens)-idx)
	for _, t := range tokens[idx+1:] {
		args = append(args, t.text)
	}
	if streamsForever(lookupProgram, args) {
		return 0, false
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
		// NOT A GAP, and never will be — see unformattable. `continue` rather than
		// declining outright, matching noiseBuiltins: in `python3 build.py && go test`
		// the reportable gap is whatever go test's row does not cover, and stopping at
		// the interpreter would hide it.
		if unformattable[h] || isScript(h) {
			continue
		}
		if reservedNames[h] {
			return "", false
		}
		if !wrappable(segs, i) {
			return "", false
		}
		if deliberatelyUnbuffered(seg.text) {
			return "", false
		}
		if deliberatelyNoFormatter(seg.text) {
			return "", false
		}
		if _, covered := matchSegment(seg.text); covered {
			return "", false
		}
		return seg.text, true
	}

	return "", false
}

// deliberatelyNoFormatter removes measured, consciously rejected candidates
// from the gap ranking. `go doc` output is the documentation/API index the
// caller requested; `-all` is explicitly exhaustive, so a formatter would save
// tokens primarily by deleting the answer rather than compressing noise.
func deliberatelyNoFormatter(text string) bool {
	tokens := tokenize(text)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) || filepathBase(shellTokenValue(tokens[idx].text)) != "go" {
		return false
	}
	idx++
	for idx < len(tokens) {
		arg := shellTokenValue(tokens[idx].text)
		if arg == "-C" {
			idx += 2
			continue
		}
		if strings.HasPrefix(arg, "-C=") {
			idx++
			continue
		}
		return arg == "doc"
	}
	return false
}

// deliberatelyUnbuffered distinguishes intentional safety exclusions from
// coverage gaps. Interactive/protocol Git invocations cannot acquire a useful
// formatter while sctx buffers to EOF, so reporting them would pollute the
// roadmap with work we must not implement.
func deliberatelyUnbuffered(text string) bool {
	tokens := tokenize(text)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) {
		return false
	}
	program := tokens[idx].text
	if i := strings.LastIndexAny(program, `/\`); i >= 0 {
		program = program[i+1:]
	}
	argv := []string{program}
	for _, token := range tokens[idx+1:] {
		argv = append(argv, token.text)
	}
	switch program {
	case "git":
		inv, ok := gitargv.Parse(argv)
		return ok && !gitargv.SafeToBuffer(inv)
	case "gh":
		inv, ok := ghargv.Parse(argv)
		return ok && !ghargv.SafeReadOnly(inv)
	case "ssh":
		for i := range argv {
			argv[i] = shellTokenValue(argv[i])
		}
		inner, ok := sshargv.RemoteCommand(argv)
		return !ok || !hookKnowsInner(inner)
	default:
		return false
	}
}

// shellTokenValue removes one matching outer quote pair for the parsers that
// need argv values rather than source spans. The original token remains in the
// command string; this copy exists only for a conservative classification.
func shellTokenValue(s string) string {
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		return s[1 : len(s)-1]
	}
	return s
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
