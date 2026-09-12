// This file implements the notice the PreToolUse rewrite hook (RunClaude,
// RunCodex) prints when the sctx SERVING the hook is not the newest
// released copy on this machine.
//
// Before this, the only way to learn a hook was stale was to voluntarily run
// `sctx doctor` — meanwhile the stale binary answered every wrapped command in
// every session, and the developer believed they were running current sctx.
// This closes that gap: the hook checks itself, at most once per
// staleHookNoticeWindow, and speaks through the SAME additionalContext
// channel the other advisory hooks use (memory.go, firstsearch.go) — never
// stderr in a way that could be mistaken for command output, and never the
// exit code or the rewrite decision.
package hook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/platform/agentsetup"
	"github.com/synapctx/sctx/internal/platform/binaries"
	"github.com/synapctx/sctx/internal/platform/config"
)

// staleHookNoticeKind keys the rate-limit row in the shared stats.db (see
// sqlite.Store.MarkNoticeIfDue). One key for both `sctx hook claude` and
// `sctx hook codex`: they run the exact same binary, so a developer with both
// clients configured hears about it once, not twice.
const staleHookNoticeKind = "stale_hook_running_binary"

// staleHookNoticeWindow is "roughly once per session/day" from the spec: a
// developer should hear this at most once while working, not on every
// wrapped command, and a machine left running overnight is worth
// re-checking the next day in case an upgrade landed since.
const staleHookNoticeWindow = 24 * time.Hour

// staleHookCheckBudget bounds the RATE-LIMIT QUERY only — a local SQLite
// read, cheap regardless of what is on PATH — never the staleness
// computation itself. That computation (agentsetup.NewestOnPath, which can
// run `<path> version` — up to binaries.go's 2s timeout — against every sctx
// on PATH) runs at MOST once per staleHookNoticeWindow, because it only runs
// when MarkNoticeIfDue has just said yes. This IS the cache: there is no
// separate "am I stale" file, only the "did I already check within window"
// row this same call just wrote, which is what makes the expensive half
// unreachable on every other invocation in the window.
const staleHookCheckBudget = 50 * time.Millisecond

// staleHookVersionNotice returns the notice line for `sctx hook claude`/
// `sctx hook codex`, or "" when nothing should be said — the rate limit has
// not elapsed, the check could not run at all (no stats.db, no home
// directory, an unreadable PATH), or the running binary already IS the
// newest non-dev copy on PATH.
//
// runningVersion is the sctx build version already handed to RunClaude/
// RunCodex, so this never re-execs the current binary to learn its own
// version (binaries.VersionOf short-circuits on that too, via isSelf, but
// there is no reason to pay even that lookup here).
func staleHookVersionNotice(runningVersion string) string {
	dbPath := statsDBPath()
	if dbPath == "" {
		return ""
	}
	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		return ""
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), staleHookCheckBudget)
	due, err := store.MarkNoticeIfDue(ctx, staleHookNoticeKind, staleHookNoticeWindow, time.Now())
	cancel()
	if err != nil || !due {
		return ""
	}

	runningBinary, err := os.Executable()
	if err != nil || runningBinary == "" {
		return ""
	}
	newest := agentsetup.NewestOnPath(os.Getenv("PATH"), sctxExeName(), runningBinary, runningVersion)
	if newest == runningBinary {
		// Already the newest non-dev copy on PATH (or the running version is a
		// dev build, which NewestOnPath refuses to redirect away from) — nothing
		// to say.
		return ""
	}
	newestVersion := binaries.VersionOf(newest)
	if newestVersion == "" {
		newestVersion = "version unknown"
	}
	return fmt.Sprintf(
		"sctx: this hook is running an older sctx (%s) — %s (%s) is on PATH. "+
			"Command output above is still being compressed correctly; you are only "+
			"missing that release's improvements. Run `sctx setup --install` to rewire it.",
		runningVersion, newest, newestVersion)
}

// sctxExeName mirrors cmd/sctx's own sctxExeName ("sctx" everywhere except
// Windows, which needs the extension for PATH resolution) — duplicated
// rather than imported because package main cannot be imported from here.
func sctxExeName() string {
	if runtime.GOOS == "windows" {
		return "sctx.exe"
	}
	return "sctx"
}

// statsDBPath mirrors spoolDir() (firstsearch.go): resolved directly rather
// than through config.Load(), which the Bash/Codex rewrite hooks deliberately
// avoid (see runHook's comment in cmd/sctx/main.go) so the rewrite itself
// stays fail-open on a machine where full configuration cannot be produced.
func statsDBPath() string {
	if p := os.Getenv("SCT__STATS_DB_PATH"); p != "" {
		return p
	}
	base, err := config.BaseDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "stats.db")
}
