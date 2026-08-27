package agentsetup

// The auto-wrap half of setup for the Kilo Code / OpenCode family.
//
// Claude Code gets a PreToolUse hook (hooks.go) and Gemini CLI gets a BeforeTool
// hook (geminihooks.go); this engine has neither, but it does have a plugin API
// whose `tool.execute.before` receives the tool's arguments as a MUTABLE object.
// Rewriting `output.args.command` there is exactly what the Claude hook does to
// `updatedInput.command`, so the same commands get wrapped in the same cases.
//
// Delivery is a plain file in the agent's own plugin directory. Verified against
// Kilo 7.4.22 on 2026-08-18: a module dropped in `<config>/plugin/*.js` is
// loaded with NO config entry and NO package install, which is what makes this
// installable rather than a documentation page telling a customer to npm-install
// something. The file calls `sctx hook rewrite`, so the rewrite rules — and the
// coverage meter behind them — stay in the binary, and a plugin from an older
// release keeps making current decisions.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pluginMarker is the first line of every plugin we write. Ownership has to be
// decidable from the file alone: a plugin we did not write must never be
// overwritten, and one we did must be updated in place when the template or the
// binary path changes.
const pluginMarker = "// sctx plugin — managed by `sctx setup`; edits inside are replaced."

// PluginStatus is the auto-wrap state for one plugin-capable agent.
type PluginStatus struct {
	AgentID   string
	AgentName string
	Path      string
	Installed bool
	// Stale is set when the file is ours but not what we would write now —
	// usually because sctx moved, which silently breaks the rewrite: the plugin
	// keeps running and keeps calling a binary that is no longer there.
	Stale bool
	// Foreign is set when a file of that name exists without our marker. We
	// report it and change nothing.
	Foreign bool
	// WiredTo is the sctx binary the installed file calls, which is not
	// necessarily the one running now, and Missing is set when that binary is
	// gone — the case where wrapping really has stopped.
	WiredTo string
	Missing string
}

// OK reports whether this agent's commands are actually being wrapped.
func (s PluginStatus) OK() bool { return s.Installed && !s.Stale && !s.Foreign }

// SctxPluginSource is the plugin module sctx installs, bound to the absolute
// path of the running binary.
//
// The path is baked in rather than resolved from PATH at run time on purpose:
// the plugin runs inside the agent's process, which does not necessarily inherit
// the shell PATH a human sees, and "sctx: command not found" inside a plugin is
// invisible — the command simply never gets wrapped and nothing says why.
func SctxPluginSource(binary string) string {
	return pluginMarker + `
//
// Rewrites covered commands to ` + "`sctx <cmd>`" + ` before the agent's bash tool runs
// them, which is what produces the token savings. Every decision — which
// commands are covered, and when wrapping would change the conclusion — is made
// by the sctx binary, never here.
//
// FAIL-OPEN, ALWAYS: any error, timeout or unexpected output leaves the command
// exactly as the agent wrote it. A plugin that can break a tool call costs the
// customer their work, which is a far worse trade than a command going
// unwrapped.

import { execFile } from "node:child_process"

const SCTX_BINARY = ` + jsString(binary) + `

function sctxRewrite(command) {
  return new Promise((resolve) => {
    try {
      execFile(
        SCTX_BINARY,
        ["hook", "rewrite", command],
        { timeout: 5000, maxBuffer: 1024 * 1024 },
        (error, stdout) => {
          if (error) return resolve(null)
          const rewritten = String(stdout || "").trim()
          resolve(rewritten === "" ? null : rewritten)
        },
      )
    } catch {
      resolve(null)
    }
  })
}

export const sctx = async () => ({
  "tool.execute.before": async (input, output) => {
    if (!input || input.tool !== "bash") return
    const command = output && output.args ? output.args.command : undefined
    if (typeof command !== "string" || command.trim() === "") return
    const rewritten = await sctxRewrite(command)
    if (rewritten && rewritten !== command) output.args.command = rewritten
  },
})
`
}

// jsString quotes a path for embedding in the generated module. Paths can hold
// quotes and backslashes; a home directory is not a promise of politeness.
func jsString(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\n", "\\n",
		"\r", "\\r",
	)
	return "\"" + replacer.Replace(value) + "\""
}

// InspectPlugin reports the auto-wrap state for one plugin-capable agent.
func InspectPlugin(home string, a Agent, binary string) (PluginStatus, error) {
	st := PluginStatus{
		AgentID:   a.ID,
		AgentName: a.Name,
		Path:      filepath.Join(home, filepath.FromSlash(a.PluginPath)),
	}
	raw, err := os.ReadFile(st.Path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("reading %s: %w", st.Path, err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), pluginMarker) {
		st.Foreign = true
		return st, nil
	}
	st.Installed = true
	st.WiredTo = wiredBinary(string(raw), `SCTX_BINARY = "`, `"`)
	// STALE MEANS "WOULD NOT WORK", NOT "NAMES A DIFFERENT PATH".
	//
	// The plugin embeds the absolute path of whichever sctx installed it, so
	// running a second copy — a dev build beside a Homebrew one — made setup
	// report a perfectly functional plugin as missing, and reinstalling it would
	// only flip the report for the other binary. What matters is whether the file
	// is ours, current for the binary it names, and whether that binary is still
	// there.
	st.Stale = strings.TrimSpace(string(raw)) != strings.TrimSpace(SctxPluginSource(st.WiredTo))
	if !st.Stale && st.WiredTo != "" {
		if _, err := os.Stat(st.WiredTo); err != nil {
			st.Stale = true
			st.Missing = st.WiredTo
		}
	}
	return st, nil
}

// wiredBinary pulls the sctx path out of something we generated. An empty
// result means we could not read it, which the caller treats as stale — the
// safe direction, since a rewrite restores a known-good file.
func wiredBinary(body, prefix, suffix string) string {
	_, after, ok := strings.Cut(body, prefix)
	if !ok {
		return ""
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, suffix)
	if !ok0 {
		return ""
	}
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(before0)
}

// InstallPlugin writes or refreshes the plugin, and never touches a file that is
// not ours.
func InstallPlugin(home string, a Agent, binary string) ([]string, error) {
	st, err := InspectPlugin(home, a, binary)
	if err != nil {
		return nil, err
	}
	if st.Foreign {
		return nil, fmt.Errorf("%s exists and was not written by sctx; left unchanged", st.Path)
	}
	if st.OK() {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(st.Path), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(st.Path), err)
	}
	if err := os.WriteFile(st.Path, []byte(SctxPluginSource(binary)), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", st.Path, err)
	}
	verb := "installed"
	if st.Installed {
		verb = "updated"
	}
	return []string{fmt.Sprintf("%s the %s auto-wrap plugin (%s)", verb, st.AgentName, st.Path)}, nil
}
