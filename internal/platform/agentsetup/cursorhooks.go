package agentsetup

// Auto-wrap for Cursor.
//
// Cursor's `preToolUse` hook (matcher `"Shell"`) is a different, newer
// contract than the older `beforeShellExecution` hook: that one is allow/deny
// only and cannot rewrite a command. `preToolUse` shipped in Cursor 1.7 (Oct
// 2025) and is verified here against the rtk reference implementation
// (github.com/rtk-ai/rtk), not Cursor's own docs page — this change had no
// WebFetch tool and cursor.com's docs render nothing usable from a plain GET.
//
// The config file is `hooks.json`, both `~/.cursor/hooks.json` (user/global)
// and `<project>/.cursor/hooks.json`. This package writes the user scope
// only, matching every other row in this file: sctx's setup model is a
// one-time machine-wide install, not a per-repository one, and nothing here
// prevents a customer from also committing a project-scoped copy by hand.
//
// The shape is its own: a `preToolUse` array (lower camelCase — NOT
// `PreToolUse`) of `{"command": ..., "matcher": "Shell"}` objects, with no
// nested `hooks` array the way Claude/Codex/Droid have. Output on a rewrite is
// Cursor's own envelope (`updated_input`, `permission`), never
// `hookSpecificOutput`.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/synapctx/sctx/internal/platform/binaries"
)

const cursorHookMatcher = "Shell"

// CursorHookStatus is the auto-wrap state for Cursor.
type CursorHookStatus struct {
	ConfigPath  string
	Installed   bool
	Stale       bool
	StaleReason string
	WiredTo     string
	Missing     string
}

func (s CursorHookStatus) Complete() bool { return s.Installed && !s.Stale }

// InspectCursorHooks reports whether Cursor is wired to rewrite its commands.
func InspectCursorHooks(home, binary string) (CursorHookStatus, error) {
	st := CursorHookStatus{ConfigPath: filepath.Join(home, ".cursor", "hooks.json")}
	doc, err := readJSONObject(st.ConfigPath)
	if err != nil {
		return st, err
	}
	entry, found := findCursorHookEntry(doc)
	st.Installed = found
	if found {
		cmd, _ := entry["command"].(string)
		st.WiredTo = wiredBinary(cmd, "", " hook cursor")
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

// InstallCursorHooks adds the sctx preToolUse entry, preserving everything
// else in hooks.json byte-for-byte where the format allows.
func InstallCursorHooks(home, binary string) ([]string, error) {
	path := filepath.Join(home, ".cursor", "hooks.json")
	doc, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	oldWired := ""
	if _, found := findCursorHookEntry(doc); found {
		st, err := InspectCursorHooks(home, binary)
		if err != nil || !st.Stale {
			return nil, err
		}
		// Stale: our own entry names a binary that moved, is gone, is a dev
		// build, or is an older release than the one running now. Replace it
		// in place rather than appending a second one.
		oldWired = st.WiredTo
		removeCursorHookEntries(doc)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc["version"] = firstNonNil(doc["version"], 1)
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	pre, _ := hooks["preToolUse"].([]any)
	pre = append(pre, map[string]any{
		"command": quoteBinaryForCommand(binary) + " hook cursor",
		"matcher": cursorHookMatcher,
	})
	hooks["preToolUse"] = pre

	if err := writeJSONObject(path, doc); err != nil {
		return nil, err
	}
	if oldWired != "" && !samePath(oldWired, binary) {
		return []string{rewireMessage("Cursor", oldWired, binary)}, nil
	}
	return []string{fmt.Sprintf("installed the Cursor auto-wrap hook (%s)", path)}, nil
}

func findCursorHookEntry(doc map[string]any) (map[string]any, bool) {
	hooks, _ := doc["hooks"].(map[string]any)
	pre, _ := hooks["preToolUse"].([]any)
	for _, e := range pre {
		entry, _ := e.(map[string]any)
		cmd, _ := entry["command"].(string)
		if invokesSctxHook(cmd, "cursor") {
			return entry, true
		}
	}
	return nil, false
}

func removeCursorHookEntries(doc map[string]any) {
	hooks, _ := doc["hooks"].(map[string]any)
	pre, _ := hooks["preToolUse"].([]any)
	kept := pre[:0]
	for _, e := range pre {
		entry, _ := e.(map[string]any)
		cmd, _ := entry["command"].(string)
		if invokesSctxHook(cmd, "cursor") {
			continue
		}
		kept = append(kept, e)
	}
	hooks["preToolUse"] = kept
}
