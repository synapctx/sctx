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
// parseVersionNumbers extracts the dotted numeric components of a version.
//
// It has to tolerate THREE shapes, because the strings being compared come from
// two independent producers: `binaries.VersionOf` returns whatever `sctx
// version` prints ("sctx v0.9.0"), while the `main.version` ldflag is the bare
// tag ("v0.9.0"), and a build off a tag carries a git suffix
// ("sctx v0.8.0-1-gbb9c1f8"). Hence the last-space trim, the `v` strip, and the
// leading-digit-run per component.
//
// EVERY REAL VERSION USED TO FAIL THIS PARSE. It split on "." and Atoi'd each
// part, so "v0.9.0" produced ["v0","9","0"] and Atoi("v0") failed — every
// comparison fell through to `a < b` on the raw strings. That is lexicographic,
// and it is right only by luck: it orders v0.8.0 before v0.9.0 correctly and
// orders v0.10.0 BEFORE v0.9.0, because "0.10" < "0.9" as text. So the first
// release after v0.9 would have silently stopped every hook rewire, reporting
// the newer binary as the older one, with nothing failing anywhere. The
// string fallback is kept for genuinely unparseable input, where it can only
// under-report staleness, but it must no longer be the normal path.
func parseVersionNumbers(v string) (nums []int, ok bool) {
	v = strings.TrimSpace(v)
	if idx := strings.LastIndex(v, " "); idx >= 0 {
		v = v[idx+1:]
	}
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if v == "" {
		return nil, false
	}
	for _, p := range strings.Split(v, ".") {
		// Only the LEADING digit run counts, so a pre-release or git suffix
		// ("0-1-gbb9c1f8", "0-rc1") contributes its number and stops the scan
		// rather than failing the whole parse. A component with no digits at
		// all ends it: "1.2.beta" is [1,2], which compares sanely against
		// [1,2,0] via the length tie-break in versionOlder.
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		if end == 0 {
			break
		}
		n, err := strconv.Atoi(p[:end])
		if err != nil {
			break
		}
		nums = append(nums, n)
		if end != len(p) {
			break
		}
	}
	return nums, len(nums) > 0
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
//
// Both binaries' versions are named alongside their paths (2026-09-12): two
// paths alone do not say WHICH one is newer or why the rewire happened, and
// that question matters more than usual now that the target is not always
// the binary running `setup` — see NewestOnPath. VersionOf is cheap here
// specifically because this only runs on the rewiring path (at most once per
// stale hook, never per invocation), not on every command.
func rewireMessage(agentLabel, oldBinary, newBinary string) string {
	return fmt.Sprintf("rewired %s hook: %s (%s) -> %s (%s)",
		agentLabel, oldBinary, orUnknownVersion(binaries.VersionOf(oldBinary)), newBinary, orUnknownVersion(binaries.VersionOf(newBinary)))
}

// orUnknownVersion is rewireMessage's fallback for a binary VersionOf could
// not identify (gone, timed out, not actually sctx) — never blank, since a
// blank pair of parens in the middle of the message reads as a bug, not a
// diagnostic.
func orUnknownVersion(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}

// NewestOnPath picks the binary a stale hook should be rewired to: the
// newest non-dev copy of exeName found on pathEnv, or runningBinary itself
// when nothing on PATH beats it.
//
// Before this, `--install` always rewired toward os.Executable() — the
// binary currently RUNNING `setup`, never mind what else is on PATH. That
// meant the one command meant to fix a stale hook could re-pin it to a
// binary that was itself stale: a developer running `~/.local/bin/sctx`
// (say, v0.8.0) with Homebrew's `sctx` (v0.9.0) ahead of it on PATH would
// have every hook rewired to the v0.8.0 copy, StaleHookReason would then call
// that "not stale" because wired == running, and `setup` would report
// success on a machine still missing v0.9.0's improvements everywhere.
//
// A dev build is NEVER outranked here, deliberately the opposite of
// StaleHookReason's own rule that a dev build always loses to a release: a
// developer working on sctx who runs `setup --install` must install THEIR
// OWN checkout's hooks, not be silently redirected to whatever release
// happens to be on PATH. runningVersion itself being empty or a dev build
// short-circuits to runningBinary for the same reason StaleHookReason's own
// runningVersion guard exists — an untrustworthy reference must not be
// replaced by a "newer" one it cannot correctly judge either.
//
// Falls back to runningBinary whenever PATH resolves nothing that beats it —
// unreadable PATH, no copy answers a version, or the newest found IS
// runningBinary — so this can only ever ADD a better target, never remove
// the one that always worked.
func NewestOnPath(pathEnv, exeName, runningBinary, runningVersion string) string {
	if runningVersion == "" || isDevBuildVersion(runningVersion) {
		return runningBinary
	}
	newest, newestVersion := runningBinary, runningVersion
	for _, path := range binaries.OnPath(pathEnv, exeName) {
		if samePath(path, runningBinary) {
			continue
		}
		v := binaries.VersionOf(path)
		if v == "" || isDevBuildVersion(v) {
			continue
		}
		if versionOlder(newestVersion, v) {
			newest, newestVersion = path, v
		}
	}
	return newest
}
