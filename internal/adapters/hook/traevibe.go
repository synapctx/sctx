// Two more hook entry points onto the shared rewrite engine, for agents whose
// envelope was pinned from a working third-party integration's source (not vendor docs). Both run the
// identical decision `sctx hook claude` does (rewriteForAgent) and are
// FAIL-OPEN: unparseable input, a non-shell tool or an unmatched command prints
// nothing and exits 0, leaving the command as the agent wrote it.
//
//   - `sctx hook trae` speaks Trae's PreToolUse hook. VERIFIED-FROM-THIRD-PARTY-SOURCE:
//     a working third-party integration's hook handler. Input is
//     `tool_name: "RunCommand"` with `tool_input.command`; output is
//     `hookSpecificOutput.updatedInput` carrying the WHOLE original tool_input
//     (Trae also sends `description`) with `command` replaced. No
//     `permissionDecision` is set: Trae keeps its own command-approval policy.
//   - `sctx hook vibe` speaks Mistral Vibe's `pre_tool` hook.
//     VERIFIED-FROM-THIRD-PARTY-SOURCE: a working third-party
//     integration's hook handler. Input has `tool_name` `bash` (or the Unified
//     Harness spelling `file_system.bash`) and `tool_input.command`; output is
//     `hook_specific_output.tool_input.command` plus a `system_message`, and
//     "no opinion" is exit 0 with EMPTY stdout.
package hook

import (
	json "encoding/json/v2"
	"fmt"
	"io"
)

type traeToolCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// RunTrae implements `sctx hook trae`.
func RunTrae(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}
	var call traeToolCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}
	if call.ToolName != "RunCommand" {
		return 0
	}
	cmd, ok := call.ToolInput["command"].(string)
	if !ok || cmd == "" {
		return 0
	}
	rewritten, ok := rewriteForAgent(cmd, version, "trae", "")
	if !ok {
		return 0
	}
	// Preserve every other tool_input member (e.g. description): updatedInput
	// replaces the model's arguments wholesale in Trae.
	call.ToolInput["command"] = rewritten
	encoded, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse",
			"updatedInput":  call.ToolInput,
		},
	})
	if err != nil {
		return 0
	}
	out.Write(append(encoded, '\n'))
	return 0
}

// RunVibe implements `sctx hook vibe`.
func RunVibe(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}
	var call traeToolCall // same two fields
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}
	if call.ToolName != "bash" && call.ToolName != "file_system.bash" {
		return 0
	}
	cmd, ok := call.ToolInput["command"].(string)
	if !ok || cmd == "" {
		return 0
	}
	rewritten, ok := rewriteForAgent(cmd, version, "vibe", "")
	if !ok {
		return 0
	}
	encoded, err := json.Marshal(map[string]any{
		"hook_specific_output": map[string]any{
			"tool_input": map[string]any{"command": rewritten},
		},
		"system_message": fmt.Sprintf("sctx: rewrote to `%s`", rewritten),
	})
	if err != nil {
		return 0
	}
	out.Write(append(encoded, '\n'))
	return 0
}
