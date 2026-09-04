// Three more entry points onto the shared rewrite engine, for agents verified
// from primary sources on 2026-09-04. Each speaks its own hook envelope but
// runs the identical decision `sctx hook claude` does (rewriteForAgent), so
// the coverage-gap meter never disagrees with itself about what a command is.
//
//   - `sctx hook cursor` speaks Cursor's `preToolUse` hook (Cursor >= 1.7,
//     Oct 2025 — NOT the older `beforeShellExecution`, which is allow/deny
//     only and cannot rewrite). Verified against the rtk reference
//     implementation (github.com/rtk-ai/rtk, src/hooks/hook_cmd.rs,
//     `run_cursor`/`cursor_allow`, and hooks/cursor/README.md) rather than
//     Cursor's own docs page, which this agent could not fetch (no WebFetch
//     tool). Input is Claude-shaped (`tool_input.command`, snake_case);
//     output is Cursor's OWN envelope — `updated_input` (snake_case), under
//     `permission: "allow"` — never `hookSpecificOutput`/`updatedInput`.
//     Cursor requires JSON on every code path, so a miss prints `{}`, not
//     nothing.
//   - `sctx hook copilot` speaks GitHub Copilot CLI's `PreToolUse` hook.
//     GitHub's own docs (docs.github.com/en/copilot/reference/hooks-reference,
//     fetched 2026-09-04) accept the event key in EITHER case
//     (`preToolUse`/`PreToolUse`) and document a camelCase
//     `toolName`/`toolArgs` (JSON-encoded string) payload answered with
//     `modifiedArgs`. rtk's source disagrees on which shape the CLI actually
//     honours: its own comment records a LIVE verification (Copilot CLI
//     1.0.73+, Linux+Windows 11) that the CLI remaps its shell tool to the
//     Claude-shaped `tool_name`/`tool_input.command` schema and honours
//     `updatedInput` there — and rtk's installer stopped registering the
//     camelCase hook for exactly that reason (a second, redundant process
//     spawn per command once both were registered). This file follows rtk's
//     live-verified path: Claude-shaped input answered with `updatedInput`.
//     The camelCase `toolName`/`toolArgs` shape is decoded too, for JetBrains'
//     Copilot plugin and any install that has not migrated off it, and is
//     answered with `modifiedArgs` per GitHub's documented contract — that
//     second path is UNVERIFIED against a live CLI by this change and is
//     recorded here as such.
//   - `sctx hook droid` speaks Factory Droid's `PreToolUse` hook (matcher
//     `Execute`). Verified against rtk (`run_droid`/`process_droid_payload`,
//     Droid v0.164.0) and docs.factory.ai/cli/configuration/hooks-guide. The
//     output envelope is byte-identical to Claude's — `updatedInput` under
//     `hookSpecificOutput`, no `permissionDecision` (Droid's own verdict flow
//     owns that) — but Droid additionally publishes deny lists
//     (`commandDenylist`/`commandBlocklist`) across up to four settings
//     scopes, and rewriting a command already on one would dodge Droid's own
//     pattern matching. This file steps aside on any match, the same way rtk
//     does.
//
// All three are FAIL-OPEN exactly like `hook claude`: unparseable input, an
// unmatched command, a denylisted command, or an internal error leaves the
// command untouched.
package hook

import (
	json "encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ---- Cursor -----------------------------------------------------------

// cursorToolCall is Cursor's preToolUse payload, decoded leniently. Cursor's
// own matcher ("Shell") already scopes invocation to the shell tool, so unlike
// Claude's tool_name this file does not gate on it — verified against rtk's
// `run_cursor`, which reads `/tool_input/command` unconditionally.
type cursorToolCall struct {
	ToolInput map[string]any `json:"tool_input"`
}

// RunCursor implements `sctx hook cursor`.
func RunCursor(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		writeCursorEmpty(out)
		return 0
	}
	var call cursorToolCall
	if err := json.Unmarshal(data, &call); err != nil {
		writeCursorEmpty(out)
		return 0
	}
	cmdVal, ok := call.ToolInput["command"]
	if !ok {
		writeCursorEmpty(out)
		return 0
	}
	cmd, ok := cmdVal.(string)
	if !ok || cmd == "" {
		writeCursorEmpty(out)
		return 0
	}
	rewritten, ok := rewriteForAgent(cmd, version, "cursor", "")
	if !ok {
		writeCursorEmpty(out)
		return 0
	}
	encoded, err := json.Marshal(map[string]any{
		"continue":   true,
		"permission": "allow",
		"updated_input": map[string]any{
			"command": rewritten,
		},
	})
	if err != nil {
		writeCursorEmpty(out)
		return 0
	}
	out.Write(append(encoded, '\n'))
	return 0
}

// writeCursorEmpty prints `{}`: Cursor requires JSON on every code path, and
// an empty stdout is read as a protocol error rather than "leave it alone".
func writeCursorEmpty(out io.Writer) {
	io.WriteString(out, "{}\n")
}

// ---- GitHub Copilot CLI -------------------------------------------------

// copilotVsCodeCall is the Claude-shaped schema: snake_case tool_name and
// tool_input.command. See the file doc comment for why this is the primary
// path this file answers with `updatedInput`.
type copilotVsCodeCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// copilotCliCall is GitHub's documented native schema: camelCase toolName and
// a JSON-ENCODED STRING toolArgs, per docs.github.com/en/copilot/reference/hooks-reference.
type copilotCliCall struct {
	ToolName string `json:"toolName"`
	ToolArgs string `json:"toolArgs"`
}

// RunCopilot implements `sctx hook copilot`.
func RunCopilot(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return 0
	}

	if _, hasSnakeCase := generic["tool_name"]; hasSnakeCase {
		var call copilotVsCodeCall
		if err := json.Unmarshal(data, &call); err != nil {
			return 0
		}
		if !isShellTool(call.ToolName) {
			return 0
		}
		cmdVal, ok := call.ToolInput["command"]
		if !ok {
			return 0
		}
		cmd, ok := cmdVal.(string)
		if !ok || cmd == "" {
			return 0
		}
		rewritten, ok := rewriteForAgent(cmd, version, "copilot-cli", "")
		if !ok {
			return 0
		}
		writeRewrite(out, rewritten)
		return 0
	}

	if _, hasCamelCase := generic["toolName"]; hasCamelCase {
		var call copilotCliCall
		if err := json.Unmarshal(data, &call); err != nil {
			return 0
		}
		if !isShellTool(call.ToolName) {
			return 0
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.ToolArgs), &args); err != nil {
			return 0
		}
		cmdVal, ok := args["command"]
		if !ok {
			return 0
		}
		cmd, ok := cmdVal.(string)
		if !ok || cmd == "" {
			return 0
		}
		rewritten, ok := rewriteForAgent(cmd, version, "copilot-cli", "")
		if !ok {
			return 0
		}
		args["command"] = rewritten
		encoded, err := json.Marshal(map[string]any{"modifiedArgs": args})
		if err != nil {
			return 0
		}
		out.Write(append(encoded, '\n'))
		return 0
	}

	return 0
}

// isShellTool accepts the tool-name spellings GitHub's docs and rtk's live
// verification both attribute to a shell invocation, in either casing.
func isShellTool(name string) bool {
	switch name {
	case "Bash", "bash", "runTerminalCommand", "run_in_terminal", "powershell", "shell", "Shell":
		return true
	}
	return false
}

// ---- Factory Droid -------------------------------------------------------

// droidToolCall is Droid's PreToolUse payload — shaped like Claude Code's
// (docs.factory.ai/cli/configuration/hooks-guide); the shell tool is matched
// as "Execute". "Bash" is tolerated defensively (Droid has no Bash tool,
// verified against v0.164.0, but the installed matcher already gates the
// invocation to Execute).
type droidToolCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// RunDroid implements `sctx hook droid`.
func RunDroid(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}
	var call droidToolCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}
	if call.ToolName != "" && call.ToolName != "Execute" && call.ToolName != "Bash" {
		return 0
	}
	cmdVal, ok := call.ToolInput["command"]
	if !ok {
		return 0
	}
	cmd, ok := cmdVal.(string)
	if !ok || cmd == "" {
		return 0
	}
	// Never rewrite past Droid's own deny lists: rewriting first would dodge
	// the pattern matching Droid would otherwise apply to the command as
	// written, exactly the reason rtk steps aside here.
	if droidDenylisted(cmd, droidDenylistPatterns()) {
		return 0
	}
	rewritten, ok := rewriteForAgent(cmd, version, "droid", "")
	if !ok {
		return 0
	}
	writeRewrite(out, rewritten)
	return 0
}

// droidDenylistPatterns collects commandDenylist/commandBlocklist across every
// Droid settings scope this process can see: the user's home directory and
// the current project directory, each with settings.json and
// settings.local.json (docs.factory.ai/cli/configuration/settings). A scope
// that does not exist is skipped, never an error.
func droidDenylistPatterns() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".factory"))
	}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		dirs = append(dirs, filepath.Join(wd, ".factory"))
	}
	var patterns []string
	for _, dir := range dirs {
		for _, file := range []string{"settings.json", "settings.local.json"} {
			raw, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				continue
			}
			var doc map[string]any
			if json.Unmarshal(raw, &doc) != nil {
				continue
			}
			for _, key := range []string{"commandDenylist", "commandBlocklist"} {
				list, _ := doc[key].([]any)
				for _, v := range list {
					s, _ := v.(string)
					s = strings.TrimSpace(s)
					if s != "" {
						patterns = append(patterns, s)
					}
				}
			}
		}
	}
	return patterns
}

// droidDenylisted reports whether cmd matches any pattern, deliberately over-
// inclusive: a spurious step-aside just means Droid's own confirm/block flow
// sees the command, which is safe, while a missed match reopens the exact
// dodge this exists to prevent. A pattern's `*` wildcards and any leading/
// trailing `:` (Droid's `curl:*` style) are stripped, and what remains is
// matched as a substring of the whitespace-normalised command.
func droidDenylisted(cmd string, patterns []string) bool {
	normCmd := strings.Join(strings.Fields(cmd), " ")
	for _, pattern := range patterns {
		core := strings.Trim(pattern, "*")
		core = strings.TrimSuffix(core, ":")
		core = strings.TrimSpace(core)
		core = strings.Join(strings.Fields(core), " ")
		if core == "" {
			// A bare "*" (or one that reduces to nothing) denies everything.
			return true
		}
		if strings.Contains(normCmd, core) {
			return true
		}
	}
	return false
}
