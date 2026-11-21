package agentsetup

// Auto-wrap for Mistral Vibe.
//
// VERIFIED-FROM-THIRD-PARTY-SOURCE: a working third-party installer and
// its README. Vibe declares hooks as `[[hooks]]` tables in
// `~/.vibe/hooks.toml` (user scope only — there is no project variant), with
// `type = "pre_tool"`, `match = "bash"`, a `command`, a `timeout` in seconds
// and `strict = false`. That installer appends at the string level to keep the user's
// comments; so does this, between the same ownership markers the Codex block
// uses, so an edited block is reported stale rather than overwritten blindly.
// It also installs a system prompt under `~/.vibe/prompts/`; that needs Vibe
// config to select it and is not replicated here.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/internal/platform/binaries"
)

// VibeHookStatus is the auto-wrap state for Mistral Vibe.
type VibeHookStatus struct {
	ConfigPath  string
	Installed   bool
	Stale       bool
	StaleReason string
	WiredTo     string
	Missing     string
}

func (s VibeHookStatus) Complete() bool { return s.Installed && !s.Stale }

func vibeHookBody(binary string) string {
	return `[[hooks]]
name = "sctx-rewrite"
type = "pre_tool"
match = "bash"
command = ` + tomlBasicString(quoteBinaryForCommand(binary)+" hook vibe") + `
timeout = 10.0
strict = false
description = "Rewrite bash commands through sctx to save tokens."
`
}

func vibeConfigPath(home string) string { return filepath.Join(home, ".vibe", "hooks.toml") }

// InspectVibeHooks reports whether Vibe is wired to rewrite its commands.
func InspectVibeHooks(home, binary string) (VibeHookStatus, error) {
	st := VibeHookStatus{ConfigPath: vibeConfigPath(home)}
	raw, err := os.ReadFile(st.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return st, fmt.Errorf("reading %s: %w", st.ConfigPath, err)
	}
	_, body, _, found, err := splitManagedBlock(string(raw), codexHooksBegin, codexHooksEnd)
	if err != nil {
		return st, fmt.Errorf("inspecting %s: %w", st.ConfigPath, err)
	}
	st.Installed = found
	if found {
		st.WiredTo = wiredBinary(body, `command = "`, ` hook vibe"`)
		st.Stale = strings.TrimSpace(body) != strings.TrimSpace(vibeHookBody(st.WiredTo))
		if st.Stale {
			st.StaleReason = "hook definition does not match what sctx would write"
		}
		if !st.Stale && st.WiredTo != "" && !samePath(st.WiredTo, binary) {
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

// InstallVibeHooks writes or refreshes the managed hook block, preserving
// everything else in hooks.toml.
func InstallVibeHooks(home, binary string) ([]string, error) {
	st, err := InspectVibeHooks(home, binary)
	if err != nil {
		return nil, err
	}
	if st.Complete() {
		return nil, nil
	}
	raw, readErr := os.ReadFile(st.ConfigPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("reading %s: %w", st.ConfigPath, readErr)
	}
	prefix, _, suffix, found, splitErr := splitManagedBlock(string(raw), codexHooksBegin, codexHooksEnd)
	if splitErr != nil {
		return nil, fmt.Errorf("updating %s: %w", st.ConfigPath, splitErr)
	}
	block := codexHooksBegin + "\n" + vibeHookBody(binary) + codexHooksEnd + "\n"
	out := prefix + block + suffix
	if !found {
		out = string(raw)
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" {
			out += "\n"
		}
		out += block
	}
	if err := os.MkdirAll(filepath.Dir(st.ConfigPath), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(st.ConfigPath), err)
	}
	if err := writePrivateFile(st.ConfigPath, []byte(out)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", st.ConfigPath, err)
	}
	if st.Installed && st.WiredTo != "" && !samePath(st.WiredTo, binary) {
		return []string{rewireMessage("Mistral Vibe", st.WiredTo, binary)}, nil
	}
	verb := "installed"
	if st.Installed {
		verb = "updated"
	}
	return []string{fmt.Sprintf("%s the Mistral Vibe auto-wrap hook (%s)", verb, st.ConfigPath)}, nil
}
