package agentsetup

// Auto-wrap for GitHub Copilot CLI.
//
// Copilot CLI reads every `*.json` file in its hooks directory
// (`~/.copilot/hooks/`, or `.github/hooks/` for a repository — this package
// installs the user scope only, same reasoning as the Cursor row) rather than
// one shared settings file, so sctx gets its OWN file:
// `~/.copilot/hooks/sctx-rewrite.json`, never touching any hook file the
// customer or another tool placed alongside it.
//
// Verified against docs.github.com/en/copilot/reference/hooks-reference
// (fetched 2026-09-04) for the directory and the PASCAL-CASE `PreToolUse` key
// (the docs accept either case) and against the rtk reference implementation
// for the entry shape and for which INPUT schema the CLI actually answers to
// — see thirdparty.go's doc comment for the full reasoning; the installed
// hook itself is schema-agnostic, since `sctx hook copilot` decodes both.
//
// A whole file is either ours or it is not — there is no marker syntax for a
// bare JSON array of hook objects the way there is for Claude's settings.json
// map, so an existing `sctx-rewrite.json` this package did not write is left
// alone and reported, never merged into.

import (
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"

	"github.com/synapctx/sctx/internal/platform/binaries"
)

const copilotHookFileName = "sctx-rewrite.json"

// CopilotHookStatus is the auto-wrap state for GitHub Copilot CLI.
type CopilotHookStatus struct {
	ConfigPath  string
	Installed   bool
	Stale       bool
	StaleReason string
	Foreign     bool // a file of this name exists that sctx did not write
	WiredTo     string
	Missing     string
}

func (s CopilotHookStatus) Complete() bool { return s.Installed && !s.Stale && !s.Foreign }

func copilotHookDoc(binary string) map[string]any {
	return map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"type":    "command",
					"command": quoteBinaryForCommand(binary) + " hook copilot",
					"cwd":     ".",
					"timeout": 5,
				},
			},
		},
	}
}

// InspectCopilotHooks reports whether Copilot CLI is wired to rewrite its
// commands.
func InspectCopilotHooks(home, binary string) (CopilotHookStatus, error) {
	st := CopilotHookStatus{ConfigPath: filepath.Join(home, ".copilot", "hooks", copilotHookFileName)}
	raw, err := os.ReadFile(st.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, fmt.Errorf("reading %s: %w", st.ConfigPath, err)
	}
	cmd, ok := copilotHookCommand(raw)
	if !ok {
		st.Foreign = true
		return st, nil
	}
	st.Installed = true
	st.WiredTo = wiredBinary(cmd, "", " hook copilot")
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
	return st, nil
}

// copilotHookCommand reports the `command` of the sole PreToolUse entry, when
// raw parses as JSON and that entry invokes an sctx copilot hook. Anything
// else — different content, an entry we did not write, a file that fails to
// parse — is "not ours" (ok=false), never an error: a hook file we cannot
// positively identify as ours must not be rewritten.
func copilotHookCommand(raw []byte) (string, bool) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", false
	}
	hooks, _ := doc["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	for _, e := range pre {
		entry, _ := e.(map[string]any)
		cmd, _ := entry["command"].(string)
		if invokesSctxHook(cmd, "copilot") {
			return cmd, true
		}
	}
	return "", false
}

// InstallCopilotHooks writes `sctx-rewrite.json`, creating
// `~/.copilot/hooks/` only when `~/.copilot` already exists (Copilot CLI was
// detected).
func InstallCopilotHooks(home, binary string) ([]string, error) {
	st, err := InspectCopilotHooks(home, binary)
	if err != nil {
		return nil, err
	}
	if st.Foreign {
		return nil, fmt.Errorf("%s exists and was not written by sctx; left unchanged", st.ConfigPath)
	}
	if st.Complete() {
		return nil, nil
	}
	if err := writeJSONObject(st.ConfigPath, copilotHookDoc(binary)); err != nil {
		return nil, err
	}
	if st.Installed && st.WiredTo != "" && !samePath(st.WiredTo, binary) {
		return []string{rewireMessage("GitHub Copilot CLI", st.WiredTo, binary)}, nil
	}
	verb := "installed"
	if st.Installed {
		verb = "updated"
	}
	return []string{fmt.Sprintf("%s the GitHub Copilot CLI auto-wrap hook (%s)", verb, st.ConfigPath)}, nil
}
