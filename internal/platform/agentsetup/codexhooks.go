package agentsetup

// Auto-wrap for Codex CLI.
//
// Codex's PreToolUse hook takes the same JSON in and the same
// `hookSpecificOutput.updatedInput.command` out as Claude Code's, so the rewrite
// itself is shared (`sctx hook codex` runs the same code as `sctx hook claude`).
// What is different is where it is configured — TOML, beside the MCP block this
// package already owns — and one thing no other client does:
//
// **Codex will not run a hook a human has not trusted.** Trust is recorded
// against the hash of the exact hook definition, so installing one leaves it
// present and INERT until the developer runs `/hooks` in Codex and trusts it.
// Reporting it as wired at that point would be the same class of lie as
// reporting an MCP server registered against a host nothing is listening on, so
// setup states the extra step instead.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/internal/platform/binaries"
)

const (
	codexHooksBegin = "# BEGIN SYNAPCTX HOOKS - managed by `sctx setup`; edits inside are replaced"
	codexHooksEnd   = "# END SYNAPCTX HOOKS"
)

// CodexHookStatus is the auto-wrap state for Codex.
type CodexHookStatus struct {
	ConfigPath string
	Installed  bool
	Stale      bool
	// StaleReason explains Stale: missing binary, a dev build, an older
	// release, or (unrelated to StaleHookReason) a hand-edited block.
	StaleReason string
	// WiredTo is the sctx binary the installed hook calls; Missing is set when
	// that binary no longer exists.
	WiredTo string
	Missing string
	// Conflicts names a PreToolUse hook OUTSIDE our block that already invokes
	// sctx. Installing a second one would wrap every command twice.
	Conflicts []string
}

// Complete reports whether our hook definition is present and current. It
// deliberately says nothing about TRUST, which lives in Codex's own state and
// which no file we write can grant.
func (s CodexHookStatus) Complete() bool {
	return s.Installed && !s.Stale && len(s.Conflicts) == 0
}

func codexHookBody(binary string) string {
	return `[[hooks.PreToolUse]]
matcher = "^Bash$"

[[hooks.PreToolUse.hooks]]
type = "command"
command = ` + tomlBasicString(quoteBinaryForCommand(binary)+" hook codex") + `
timeout = 10
statusMessage = "sctx: wrapping command"
`
}

// InspectCodexHooks reports whether Codex is wired to rewrite its commands.
func InspectCodexHooks(home, binary string) (CodexHookStatus, error) {
	st := CodexHookStatus{ConfigPath: filepath.Join(home, ".codex", "config.toml")}
	raw, err := os.ReadFile(st.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return st, fmt.Errorf("reading %s: %w", st.ConfigPath, err)
	}
	prefix, body, suffix, found, err := splitManagedBlock(string(raw), codexHooksBegin, codexHooksEnd)
	if err != nil {
		return st, fmt.Errorf("inspecting %s: %w", st.ConfigPath, err)
	}
	st.Installed = found
	if found {
		// Same rule as the plugin: the hook names whichever sctx installed it,
		// and naming another copy is not a fault by itself. It is stale when
		// the block does not match what we would write for its OWN wired
		// binary (a hand edit, or a template change), when that binary is
		// gone, OR — the check that used to be entirely missing — when it is
		// a dev build or older than the sctx running `setup` now.
		st.WiredTo = wiredBinary(body, `command = "`, ` hook codex"`)
		st.Stale = strings.TrimSpace(body) != strings.TrimSpace(codexHookBody(st.WiredTo))
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
	if outside := prefix + "\n" + suffix; strings.Contains(outside, "hooks.PreToolUse") && strings.Contains(outside, "hook codex") {
		st.Conflicts = append(st.Conflicts, "a PreToolUse hook outside our block already runs sctx")
	}
	return st, nil
}

// InstallCodexHooks writes or refreshes the managed hook block, preserving
// everything else in the file.
func InstallCodexHooks(home, binary string) ([]string, error) {
	st, err := InspectCodexHooks(home, binary)
	if err != nil {
		return nil, err
	}
	if st.Complete() {
		return nil, nil
	}
	if len(st.Conflicts) > 0 {
		return nil, fmt.Errorf("%s: %s; left unchanged", st.ConfigPath, strings.Join(st.Conflicts, ", "))
	}
	raw, readErr := os.ReadFile(st.ConfigPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("reading %s: %w", st.ConfigPath, readErr)
	}
	prefix, _, suffix, found, splitErr := splitManagedBlock(string(raw), codexHooksBegin, codexHooksEnd)
	if splitErr != nil {
		return nil, fmt.Errorf("updating %s: %w", st.ConfigPath, splitErr)
	}
	block := codexHooksBegin + "\n" + codexHookBody(binary) + codexHooksEnd + "\n"
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
		return []string{rewireMessage("OpenAI Codex CLI", st.WiredTo, binary) + " — run /hooks in Codex once to trust it"}, nil
	}
	verb := "installed"
	if st.Installed {
		verb = "updated"
	}
	return []string{fmt.Sprintf("%s the OpenAI Codex CLI auto-wrap hook (%s) — run /hooks in Codex once to trust it",
		verb, st.ConfigPath)}, nil
}
