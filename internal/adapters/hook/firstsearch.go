package hook

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
)

// RunClaudeFirstSearch implements `sctx hook claude-first-search`: a Claude Code
// PreToolUse hook on Grep|Glob|Agent that, on the FIRST TWO local searches of a
// session, points at the organization-wide graph.
//
// The moment is the whole point. An agent about to grep has just decided it has
// a "where is X" question — the one question the graph answers better and across
// repositories the checkout does not contain. The session brief already said so
// at startup, but startup context decays, and the search is the first evidence
// that it decayed.
//
// It stops after two. A nudge on every search is a tax on the tool the developer
// chose, and an agent that has ignored the same sentence twice will ignore it a
// third time — at that point it is noise with a per-turn cost.
//
// NO NETWORK CALL, ever. This sits in PreToolUse, before a tool the agent is
// waiting on; the whole hook must be a file read, an increment and a print.
const firstSearchNudges = 2

// sessionCounterTTL bounds the counter directory. Session ids are unbounded over
// a machine's lifetime, so without this the directory grows forever — and a
// week-old counter cannot inform a live session anyway.
const sessionCounterTTL = 7 * 24 * time.Hour

type firstSearchCall struct {
	SessionID string         `json:"session_id"`
	CWD       string         `json:"cwd"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// RunClaudeFirstSearch always returns 0: a PreToolUse hook that can fail is a
// PreToolUse hook that can block a search.
func RunClaudeFirstSearch(in io.Reader, out io.Writer, cfg config.Config) int {
	if os.Getenv("SCT__MEMORY_SURFACING_DISABLED") == "true" {
		return 0
	}
	data, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return 0
	}
	var call firstSearchCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}

	// The root is carried alongside the name so a project-scope `.mcp.json` can
	// be consulted for the server name (see claudeServerNameFor).
	root, repo, _ := gitrepo.RootAndName(call.CWD)
	if repo == "" {
		return 0
	}
	org := orgOf(repo)
	token, _ := cfg.TokenForOrg(org)
	if token == "" {
		return 0
	}

	// Counted BEFORE the budget check so the count stays true: a session's third
	// search is the third whether or not we spoke on it.
	n := bumpSessionSearchCount(call.SessionID)
	if n < 1 || n > firstSearchNudges {
		return 0
	}

	home, _ := os.UserHomeDir()
	server := claudeServerNameFor(home, call.CWD, root, token)
	guessed := server == ""
	if guessed {
		server = org
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SynapCTX: local search #%d this session, in org %s. "+
		"The organization's whole code graph answers \"where is X / who calls Y / how does Z work\" across every repository in one call — %s — and %s holds what teammates decided and no file says. "+
		"Use them for orientation; keep local search for exact bytes of a file you already know.",
		n, org, toolName(server, "retrieve_context", guessed), toolName(server, "recall_memory", guessed))

	// The find_references sentence is added only for a pattern that is actually a
	// SYMBOL, reusing the same precision gate as the post-tool grep nudge: a
	// suggestion to look up call sites for a regex or a phrase is an advert.
	if call.ToolName == "Grep" {
		if pattern, _ := call.ToolInput["pattern"].(string); isPlainIdentifier(pattern) {
			fmt.Fprintf(&b, " Before changing %s, find_references lists every call site across repositories; grep cannot.", pattern)
		}
	}

	// additionalContext ALONE, with no permissionDecision. Returning
	// `"allow"` here reads as an affirmative auto-approval and would suppress a
	// developer's own `ask` rule on Agent for the first two spawns of every
	// session — a hook that only wants to add a sentence must never widen a
	// permission. Omitting the field defers to the normal permission flow.
	//
	// Same writer as the PostToolUse hook, parameterised by event: the host
	// silently discards an envelope whose hookEventName does not match.
	writeAdditionalContext(out, "PreToolUse", b.String())
	return 0
}

// bumpSessionSearchCount returns this session's search number, 1-based.
//
// Read-modify-write with no locking, which is correct for what this is: one
// developer, one agent, and PreToolUse hooks for a single session do not run
// concurrently. A lost update would cost one extra nudge, which is the cheapest
// failure in this file — worth far less than a lock file in a session's hot path.
//
// Returns 0 on any failure, which the caller reads as "say nothing": a counter
// we cannot maintain is one that could nudge on every search forever.
func bumpSessionSearchCount(sessionID string) int {
	id := sanitizeSessionID(sessionID)
	if id == "" {
		return 0
	}
	spool := spoolDir()
	if spool == "" {
		return 0
	}
	dir := filepath.Join(spool, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0
	}
	pruneOldSessionCounters(dir)

	path := filepath.Join(dir, id)
	n := 0
	if raw, err := os.ReadFile(path); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	n++
	if err := os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644); err != nil {
		return 0
	}
	return n
}

// sanitizeSessionID makes a client-supplied string safe to use as a FILENAME.
// The session id comes from outside sctx and is interpolated into a path, so
// anything outside this set — a separator, a "..", a NUL — is dropped rather
// than escaped. An id that survives to nothing returns "" and the hook stays
// silent.
func sanitizeSessionID(id string) string {
	if len(id) > 128 {
		id = id[:128]
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	// A name of only dots would still address a directory.
	if strings.Trim(b.String(), ".") == "" {
		return ""
	}
	return b.String()
}

// pruneOldSessionCounters is opportunistic housekeeping: every error is ignored,
// because failing to tidy is never a reason to withhold the nudge.
func pruneOldSessionCounters(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-sessionCounterTTL)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// spoolDir resolves the same directory the Bash hook spools telemetry into, so
// everything sctx writes on a machine lives under one path a developer can
// delete. Returns "" when no home can be resolved.
func spoolDir() string {
	if dir := os.Getenv("SCT__SPOOL_DIR"); dir != "" {
		return dir
	}
	base, err := config.BaseDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "spool")
}
