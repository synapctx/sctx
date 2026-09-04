package hook

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/platform/config"
)

// repeatedRunThreshold is how many times, in one session, a command must
// have already produced the exact same raw output size before the nudge
// fires. Below this a repeat is unremarkable — someone re-running a build
// once or twice after a small edit is normal workflow.
const repeatedRunThreshold = 3

// maxNudgesPerSession bounds how many times THIS hook speaks in one
// session, across every distinct command — a session that trips the
// threshold on several different commands still hears about it at most this
// many times, so a genuinely thrashing session does not turn into a wall of
// identical-looking lines.
const maxNudgesPerSession = 3

// repeatNudgeBudget is the whole allowance for the local stats.db lookup
// this nudge does: two indexed-scale queries against a per-developer
// SQLite file with no network involved. Generous relative to the actual
// cost, tight relative to the PostToolUse hook's own overall latency
// ceiling — this sits in the edit loop alongside the memory/symbol nudges.
const repeatNudgeBudget = 50 * time.Millisecond

// repeatNudgeAllowlist names (program, subcommand) pairs that legitimately
// run identically many times in a session and must NEVER trigger this
// nudge: re-reading the same file or re-checking the same state is normal
// workflow, not wasted rework. A program with no subcommand concept (ls,
// cat, head, tail) is keyed on "".
var repeatNudgeAllowlist = map[string]map[string]bool{
	"git":     {"status": true, "log": true, "diff": true},
	"kubectl": {"get": true},
	"ls":      {"": true},
	"cat":     {"": true},
	"head":    {"": true},
	"tail":    {"": true},
}

// isAllowlistedRepeat reports whether the normalized argv's (program,
// subcommand) pair is one that is expected to repeat and must never nudge.
func isAllowlistedRepeat(argv string) bool {
	fields := strings.Fields(argv)
	if len(fields) == 0 {
		return false
	}
	subs, ok := repeatNudgeAllowlist[basenameOf(fields[0])]
	if !ok {
		return false
	}
	if subs[""] {
		return true
	}
	return len(fields) >= 2 && subs[fields[1]]
}

// basenameOf strips a directory prefix and a Windows .exe suffix, mirroring
// alreadyWrapped's own normalization, so `/usr/bin/git` and `git.exe` match
// the same allowlist entry as `git`.
func basenameOf(token string) string {
	token = strings.Trim(token, `"'`)
	if i := strings.LastIndexAny(token, `/\`); i >= 0 {
		token = token[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSuffix(token, ".exe"), ".EXE")
}

// normalizeSessionArgv turns ONE segment's raw text into the same shape
// stats.Run.Argv is keyed on. The PreToolUse rewrite hook prefixes a wrapped
// segment with "sctx " (see Rewrite), so PostToolUse sees "sctx go vet ./..."
// for the very command stats.db recorded as "go vet ./..." — the leading
// sctx token is stripped before comparing. Redirect tokens (`2>&1`, `>out`,
// `<in`) are dropped too: they are consumed by the shell before argv ever
// reaches the wrapped program, so they are never part of stats.db's argv
// either, and leaving them in made every redirected command an automatic
// non-match. Multi-segment commands (&&, |, ;) are handled by the caller,
// sessionArgvCandidates, which splits first and calls this per segment.
func normalizeSessionArgv(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	if basenameOf(fields[0]) == "sctx" {
		fields = fields[1:]
	}
	var kept []string
	for _, f := range fields {
		if strings.ContainsAny(f, "<>") {
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

// sessionArgvCandidates splits rawCommand into its `;`/`&&`/`||`/`|`-separated
// segments (reusing splitSegments, the same quote- and escape-aware scanner
// the PreToolUse rewrite hook itself uses — see rewrite.go) and returns the
// normalized argv of every segment that hook would treat as a wrapping
// candidate: a pipeline head, free of disallowed redirects, whose only
// downstream pipe stages (if any) are line-narrowing (wrappable, also from
// rewrite.go — the very check that lets `go test ./... 2>&1 | tail -50`
// rewrite while `go test ./... | grep FAIL` does not). This is what makes the
// nudge segment-aware: `go test ./x 2>&1 | tail -1` used to be compared to
// stats.db as one literal string that never matched anything, because the
// run pipeline only ever records the WRAPPED SEGMENT's own argv
// ("go test ./x"), never the pipeline around it.
//
// Order is left-to-right, matching the command as written; the caller stops
// at the first candidate that actually qualifies. Returns nil — no
// candidates, so no nudge — for anything splitSegments cannot parse
// confidently. Fail-open: a parse doubt costs a missed nudge, never a wrong
// one.
func sessionArgvCandidates(rawCommand string) []string {
	segs, ok := splitSegments(rawCommand)
	if !ok {
		return nil
	}
	var out []string
	for i, seg := range segs {
		if !wrappable(segs, i) {
			continue
		}
		if argv := normalizeSessionArgv(seg.text); argv != "" {
			out = append(out, argv)
		}
	}
	return out
}

// repeatNudgeState is the per-session record of which argv values this hook
// has already nudged about, persisted so the (session, argv) rate limit
// survives across the many separate hook process invocations one session
// makes.
type repeatNudgeState struct {
	Nudged []string `json:"nudged"`
}

func (st repeatNudgeState) alreadyNudged(argv string) bool {
	for _, a := range st.Nudged {
		if a == argv {
			return true
		}
	}
	return false
}

// repeatNudgeStatePath returns where this session's state lives, or "" when
// no spool directory can be resolved — callers read that as "say nothing".
// Deliberately a SEPARATE file from firstsearch's own per-session counter
// (sessions/<id>), rather than a second field squeezed into it: the two
// features have unrelated lifecycles and shapes, and sharing a file would
// make one feature's read-modify-write race the other's.
func repeatNudgeStatePath(id string) string {
	spool := spoolDir()
	if spool == "" {
		return ""
	}
	return filepath.Join(spool, "sessions", id+".repeat")
}

func loadRepeatNudgeState(id string) repeatNudgeState {
	var st repeatNudgeState
	path := repeatNudgeStatePath(id)
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

// saveRepeatNudgeState is best-effort: a failed write costs at most one
// extra nudge later, never a wrong answer now.
func saveRepeatNudgeState(id string, st repeatNudgeState) {
	path := repeatNudgeStatePath(id)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// repeatedRunNudge implements item 4 of the 2026-09-04 roadmap: after a
// wrapped Bash command runs, if any of its wrappable segments' normalized
// argv (see sessionArgvCandidates) has already produced the exact same raw
// output size at least repeatedRunThreshold times in this session, return
// one line asking the agent to stop re-running it unchanged. Segments are
// evaluated left to right and AT MOST ONE nudges per call — the first
// qualifying segment wins, so `cd dir && go vet ./... && go test ./...`
// nudges on whichever of go vet/go test actually repeated, never both at
// once. Empty return means "say nothing", for any reason at all — unknown
// session, nothing splitSegments could parse confidently, no wrappable
// segment, every candidate allowlisted or already nudged, per-session cap
// reached, no stats.db, or fewer than the threshold repeats.
//
// Local-only: the one dependency is this machine's own stats.db, opened
// read-only in effect (two SELECTs per candidate) under repeatNudgeBudget
// each. No network call and no API key are needed, unlike the
// memory/symbol nudges this shares a hook process with.
func repeatedRunNudge(cfg config.Config, sessionID, rawCommand string) string {
	id := sanitizeSessionID(sessionID)
	if id == "" {
		return ""
	}
	candidates := sessionArgvCandidates(rawCommand)
	if len(candidates) == 0 {
		return ""
	}

	state := loadRepeatNudgeState(id)
	if len(state.Nudged) >= maxNudgesPerSession {
		return ""
	}

	store, err := sqlite.NewStore(cfg.StatsDBPath)
	if err != nil {
		return ""
	}
	defer store.Close()

	for _, argv := range candidates {
		if isAllowlistedRepeat(argv) || state.alreadyNudged(argv) {
			continue
		}

		count, ok := identicalRunCount(store, id, argv)
		if !ok || count < repeatedRunThreshold {
			continue
		}

		state.Nudged = append(state.Nudged, argv)
		saveRepeatNudgeState(id, state)

		return fmt.Sprintf("sctx: `%s` has produced identical output %d times this session; run it once per code change.", argv, count)
	}
	return ""
}

// identicalRunCount looks up how many times argv has already produced the
// same raw output size in this session, each call under its own
// repeatNudgeBudget deadline so one slow candidate in a long pipeline cannot
// eat the budget meant for the next one. ok is false for any lookup failure
// (no prior run recorded, or a query error) — the caller reads that as
// "nothing to say about this candidate", not an error.
func identicalRunCount(store *sqlite.Store, sessionID, argv string) (count int64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), repeatNudgeBudget)
	defer cancel()

	rawBytes, found, err := store.LatestRawBytes(ctx, sessionID, argv)
	if err != nil || !found {
		return 0, false
	}
	n, err := store.IdenticalRunCount(ctx, sessionID, argv, rawBytes)
	if err != nil {
		return 0, false
	}
	return n, true
}
