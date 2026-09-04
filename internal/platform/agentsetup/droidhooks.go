package agentsetup

// Auto-wrap for Factory Droid.
//
// Droid's PreToolUse hook (matcher `"Execute"`) uses the SAME entry shape as
// Claude Code's — `{"matcher": ..., "hooks": [{"type": "command", "command":
// ...}]}` — which is why this file reuses `hookPresent`/`addHook` from
// hooks.go rather than re-deriving them. What differs is the FILE: Droid keeps
// its events at the ROOT of `hooks.json` (no surrounding `"hooks"` key), with
// a `settings.json` fallback whose `hooks` key Droid merges hooks.json OVER,
// per event — verified against docs.factory.ai/cli/configuration/hooks-guide
// and the rtk reference implementation (Droid v0.140-v0.164).
//
// This install always targets `hooks.json`: it is the file Droid's own
// `/hooks` UI reads and writes, and installing into `settings.json` instead
// would be silently shadowed the moment a root `hooks.json` with a PreToolUse
// entry appears. If `settings.json` already carries a LIVE PreToolUse hook and
// no `hooks.json` exists, this still creates `hooks.json` — Droid loads
// whichever file defines PreToolUse first in the precedence rtk documents,
// which is `hooks.json`, so installing there is always the entry that wins.
//
// Droid also publishes command deny lists (`commandDenylist`/
// `commandBlocklist`) that `sctx hook droid` reads at RUN time (see
// thirdparty.go); nothing here needs to know about them; installing the hook
// and honouring a deny list are separate concerns.

import (
	"fmt"
	"os"
	"path/filepath"
)

// DroidHookStatus is the auto-wrap state for Factory Droid.
type DroidHookStatus struct {
	ConfigPath string
	Installed  bool
	Stale      bool
	WiredTo    string
	Missing    string
}

func (s DroidHookStatus) Complete() bool { return s.Installed && !s.Stale }

func droidSpec(binary string) HookSpec {
	return HookSpec{
		Event:      "PreToolUse",
		Matcher:    "Execute",
		Subcommand: "droid",
		Command:    quoteBinaryForCommand(binary) + " hook droid",
	}
}

// InspectDroidHooks reports whether Droid is wired to rewrite its commands.
func InspectDroidHooks(home, binary string) (DroidHookStatus, error) {
	st := DroidHookStatus{ConfigPath: filepath.Join(home, ".factory", "hooks.json")}
	doc, err := readJSONObject(st.ConfigPath)
	if err != nil {
		return st, err
	}
	// The root layout puts the event map at the top level, so it is wrapped
	// as {"hooks": doc} to reuse hookPresent/addHook, which both expect a
	// document with a "hooks" key.
	wrapped := map[string]any{"hooks": any(doc)}
	spec := droidSpec(binary)
	st.Installed = hookPresent(wrapped, spec)
	if st.Installed {
		st.WiredTo = binary
		if _, err := os.Stat(binary); err != nil {
			st.Stale = true
			st.Missing = binary
		}
	}
	return st, nil
}

// InstallDroidHooks adds the sctx PreToolUse/Execute entry to hooks.json,
// preserving everything else.
func InstallDroidHooks(home, binary string) ([]string, error) {
	path := filepath.Join(home, ".factory", "hooks.json")
	doc, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	spec := droidSpec(binary)
	wrapped := map[string]any{"hooks": any(doc)}
	if hookPresent(wrapped, spec) {
		return nil, nil
	}
	addHook(wrapped, spec)
	// addHook may have created the "hooks" key on wrapped (a fresh map, not
	// doc) if doc had none; pull it back out so doc itself carries the events
	// at its root, per Droid's layout.
	if events, ok := wrapped["hooks"].(map[string]any); ok {
		doc = events
	}
	if err := writeJSONObject(path, doc); err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("installed the Factory Droid auto-wrap hook (%s)", path)}, nil
}
