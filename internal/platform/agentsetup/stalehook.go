package agentsetup

// Shared "is this hook wired to a binary --install should replace" rule,
// used by every hook/plugin mechanism (Claude/Gemini's settings.json,
// Codex's TOML, Cursor/Copilot/Droid's own JSON files, and the Kilo/OpenCode
// plugin's SCTX_BINARY).
//
// Before this, "an existing sctx hook is preserved" meant exactly that: any
// entry that invoked `sctx hook <subcommand>` counted as installed forever,
// even when the binary it named was a `dev` build or a release several
// versions behind the one running `setup` now. A hook first wired from
// `~/.local/bin/sctx` (a dev build) stayed wired to it after `sctx init` or a
// Homebrew upgrade put a newer release on PATH, because nothing compared the
// two — presence was the only test. `StaleHookReason` is the comparison that
// was missing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/platform/binaries"
)

// isDevBuildVersion reports whether a `<binary> version` answer is the
// unreleased build ("sctx dev"). Kept as its own copy of cmd/sctx's
// isDevVersion: package main cannot be imported from a platform package, and
// the rule is small enough that duplicating it beats threading a callback
// through every hook file.
func isDevBuildVersion(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasSuffix(v, " dev") || v == "dev"
}

// parseVersionNumbers extracts the dot-separated numeric components from a
// `<binary> version` answer ("sctx 0.7.0" -> [0, 7, 0]), taking whatever
// follows the LAST space so a "sctx " (or any other program name) prefix is
// ignored. ok is false when that tail does not parse as all-numeric dotted
// components (e.g. "dev", or an answer this binary never produced).
func parseVersionNumbers(v string) (nums []int, ok bool) {
	v = strings.TrimSpace(v)
	if idx := strings.LastIndex(v, " "); idx >= 0 {
		v = v[idx+1:]
	}
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	nums = make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	return nums, true
}

// versionOlder reports whether a is an older release than b. Falls back to a
// plain string comparison when either side does not parse as dotted numeric
// components — which can only ever under-report staleness, never wrongly
// flag a healthy install.
func versionOlder(a, b string) bool {
	an, aok := parseVersionNumbers(a)
	bn, bok := parseVersionNumbers(b)
	if !aok || !bok {
		return a < b
	}
	for i := 0; i < len(an) && i < len(bn); i++ {
		if an[i] != bn[i] {
			return an[i] < bn[i]
		}
	}
	return len(an) < len(bn)
}

// samePath reports whether two binary paths identify the same file, by
// resolved (symlink-free) path when both resolve, otherwise by exact string
// equality — a path that fails to resolve (already gone) can still be
// recognised as literally the one just handed in.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, aerr := filepath.EvalSymlinks(a)
	rb, berr := filepath.EvalSymlinks(b)
	return aerr == nil && berr == nil && ra == rb
}

// StaleHookReason reports why a hook currently wired to wiredBinary should be
// rewired to runningBinary (the sctx executable running `setup` now, whose
// own reported version is runningVersion) — or "" when it should be left
// exactly as it is.
//
// A hook already pointing at the exact binary running now is never stale
// regardless of what its version STRING happens to say: two invocations of
// the same file cannot disagree, and re-running `<path> version` is wasted
// work. Otherwise it is stale when at least one holds:
//
//   - the wired binary no longer exists (moved, uninstalled, a temp checkout
//     that is gone);
//   - it reports a dev build ("sctx dev") — a developer's own checkout must
//     never keep winning a hook once a real release is on this machine;
//   - it reports an older release version than runningVersion.
//
// Naming a DIFFERENT but equally current release (e.g. two Homebrew
// installs on separate PATH entries) is deliberately NOT stale by this rule
// alone — only dev-ness, an older version or a missing binary are.
func StaleHookReason(wiredBinary, runningBinary, runningVersion string) (reason string, stale bool) {
	if wiredBinary == "" || runningBinary == "" {
		return "", false
	}
	if samePath(wiredBinary, runningBinary) {
		return "", false
	}
	if _, err := os.Stat(wiredBinary); err != nil {
		return "the sctx it calls no longer exists", true
	}
	if runningVersion == "" || isDevBuildVersion(runningVersion) {
		// The running binary is not itself a trustworthy "newer" reference
		// (e.g. this invocation of setup IS a dev build) — never rewire
		// toward it purely on version grounds.
		return "", false
	}
	wiredVersion := binaries.VersionOf(wiredBinary)
	if isDevBuildVersion(wiredVersion) {
		return "the sctx it calls is a dev build", true
	}
	if wiredVersion != "" && wiredVersion != runningVersion && versionOlder(wiredVersion, runningVersion) {
		return "the sctx it calls (" + wiredVersion + ") is older than the running " + runningVersion, true
	}
	return "", false
}

// rewireMessage is the standard `--install` progress line printed whenever a
// hook's wired binary is replaced because StaleHookReason found it stale.
func rewireMessage(agentLabel, oldBinary, newBinary string) string {
	return fmt.Sprintf("rewired %s hook: %s -> %s", agentLabel, oldBinary, newBinary)
}
