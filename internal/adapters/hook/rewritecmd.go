package hook

// Two more entry points onto the same rewrite engine `sctx hook claude` uses,
// for the two other ways an agent lets something intervene before it runs a
// command.
//
//   - `sctx hook rewrite <cmd>` prints the rewritten command as PLAIN TEXT, for
//     callers that are not speaking anyone's hook protocol — today the JS plugin
//     sctx installs into Kilo Code and OpenCode, which mutates the tool's
//     arguments in process and only needs the string.
//   - `sctx hook gemini` speaks Gemini CLI's BeforeTool contract, whose
//     `hookSpecificOutput.tool_input` merges over the model's arguments.
//
// Both are FAIL-OPEN in the same way as the Claude hook: any unparseable input,
// unmatched command or internal error prints nothing and exits 0, leaving the
// command exactly as the agent wrote it. A hook that can break the agent's tool
// call is worse than no hook, because the failure lands on the customer's work
// rather than on ours.

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"strings"
)

// RunRewrite implements `sctx hook rewrite <command>`.
//
// The command arrives as ARGV, not stdin, and that is deliberate: the callers
// are shells-in-a-plugin where quoting a multi-line command into a pipe is the
// likeliest place to corrupt it, while an exec with one argument cannot. It is
// not a secret — it is the command the agent is about to run in the clear.
//
// Output is the rewritten command and nothing else, or empty when there is no
// rewrite, so a caller can treat "no output" as "leave it alone" without
// parsing anything.
func RunRewrite(args []string, out io.Writer, version string) int {
	cmd := strings.Join(args, " ")
	if strings.TrimSpace(cmd) == "" {
		return 0
	}
	// The plugin speaks for both Kilo Code and OpenCode through the same
	// verb, and the argv it hands us carries no agent or session identity —
	// that hand-off rides on the plugin's own `shell.env` hook instead (see
	// agentsetup's plugin.go), which sets SCT__CLIENT/SCT__SESSION directly in
	// the WRAPPED command's environment rather than through this file-based
	// fallback. Nothing to hand off here.
	if rewritten, ok := rewriteForAgent(cmd, version, "unknown", ""); ok {
		fmt.Fprintln(out, rewritten)
	}
	return 0
}

// geminiToolCall is Gemini CLI's BeforeTool payload, decoded leniently: only the
// tool name and the shell command matter, and an unknown field must never make
// this fail.
type geminiToolCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// RunGemini implements `sctx hook gemini`, the BeforeTool hook for Gemini CLI's
// run_shell_command tool.
//
// The contract differs from Claude's in two ways that matter. The payload is
// snake_case (`tool_name`, `tool_input`), and the override rides on
// `hookSpecificOutput.tool_input`, which Gemini MERGES over the model's
// arguments — so we send only `command` and leave every other argument alone.
// Gemini also requires stdout to hold JSON and nothing else, which is why
// diagnostics here go nowhere rather than to stdout.
func RunGemini(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}
	var call geminiToolCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}
	if call.ToolName != "run_shell_command" {
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
	rewritten, ok := rewriteForAgent(cmd, version, "gemini-cli", "")
	if !ok {
		return 0
	}
	encoded, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "BeforeTool",
			"tool_input": map[string]any{
				"command": rewritten,
			},
		},
	})
	if err != nil {
		return 0
	}
	fmt.Fprintln(out, string(encoded))
	return 0
}

// rewriteForAgent is the shared body of every pre-tool hook: apply the same
// rules, and record the same coverage meter, whichever agent asked.
//
// Keeping this in one place is what stops the meter from disagreeing with
// itself. A `go test` that Kilo runs and a `go test` that Claude runs are the
// same command from the same customer, and the ranking of what to build next
// must not depend on which client happened to be open.
func rewriteForAgent(cmd, version, agent, sessionID string) (string, bool) {
	if os.Getenv("SCT__REWRITE_DISABLED") == "true" {
		return "", false
	}
	writeSessionHandoff(agent, sessionID)
	root, matchers := trustedProjectMatchers()
	if rewritten, ok := rewriteWithProject(cmd, root, matchers); ok {
		return rewritten, true
	}
	if seg, ok := gapSegment(cmd); ok {
		spoolCoverageGap(seg, version)
	} else if seg, reason, ok := declineSegment(cmd); ok {
		spoolCoverageDecline(seg, reason, version)
	}
	return "", false
}
