package agentsetup

// Auto-wrap for Factory Droid.
//
// Droid's PreToolUse hook (matcher `"Execute"`) uses the SAME entry shape as
// Claude Code's — `{"matcher": ..., "hooks": [{"type": "command", "command":
// ...}]}` — which is why this file reuses `findHookEntry`/`addHook` from
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

	"github.com/synapctx/sctx/internal/platform/binaries"
)

// DroidHookStatus is the auto-wrap state for Factory Droid.
type DroidHookStatus struct {
	ConfigPath  string
	Installed   bool
	Stale       bool
	StaleReason string
	WiredTo     string
	Missing     string
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
	// as {"hooks": doc} to reuse findHookEntry/addHook, which both expect a
	// document with a "hooks" key.
	wrapped := map[string]any{"hooks": any(doc)}
	spec := droidSpec(binary)
	entry, found := findHookEntry(wrapped, spec)
	st.Installed = found
	if found {
		cmd, _ := entry["command"].(string)
		st.WiredTo = wiredBinary(cmd, "", " hook droid")
		if st.WiredTo != "" && !samePath(st.WiredTo, binary) {
			if _, err := os.Stat(st.WiredTo); err != nil {
				st.Stale = true
				st.Missing = st.WiredTo
				st.StaleReason = "the sctx it calls no longer exists"
			} else if reason, stale := StaleHookReason(st.WiredTo, binary, binaries.VersionOf(binary)); stale {
				st.Stale = true
				st.StaleReason = reason
			}
		}
	}
	return st, nil
}

// InstallDroidHooks adds the sctx PreToolUse/Execute entry to hooks.json,
// preserving everything else, and REWIRES an existing entry in place when it
// is stale (see StaleHookReason) rather than leaving it wired to a dev build
// or an older release forever.
func InstallDroidHooks(home, binary string) ([]string, error) {
	path := filepath.Join(home, ".factory", "hooks.json")
	doc, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	spec := droidSpec(binary)
	wrapped := map[string]any{"hooks": any(doc)}
	if entry, found := findHookEntry(wrapped, spec); found {
		st, err := InspectDroidHooks(home, binary)
		if err != nil {
			return nil, err
		}
		if !st.Stale {
			return nil, nil
		}
		entry["command"] = spec.Command
		if events, ok := wrapped["hooks"].(map[string]any); ok {
			doc = events
		}
		if err := writeJSONObject(path, doc); err != nil {
			return nil, err
		}
		return []string{rewireMessage("Factory Droid", st.WiredTo, binary)}, nil
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
