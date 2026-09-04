package hook

import (
	"bytes"
	"strings"
	"testing"
)

// Fixture shapes below are hand-built from the sources cited in
// thirdparty.go's doc comment, not captured from a live agent — none of the
// three ships on this machine. Each still pins the exact field names a real
// payload would carry, so a schema change in rewrite.go surfaces here.

func TestRunCursorRewritesAMatchedCommand(t *testing.T) {
	in := strings.NewReader(`{"tool_input":{"command":"git status"}}`)
	var out bytes.Buffer
	if code := RunCursor(nil, in, &out, "test"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, `"updated_input"`) || !strings.Contains(got, "sctx git status") {
		t.Errorf("output = %q, want updated_input carrying the rewritten command", got)
	}
	if !strings.Contains(got, `"permission":"allow"`) {
		t.Errorf("output = %q, want permission: allow", got)
	}
}

func TestRunCursorPrintsEmptyObjectOnAMiss(t *testing.T) {
	in := strings.NewReader(`{"tool_input":{"command":"echo hi"}}`)
	var out bytes.Buffer
	RunCursor(nil, in, &out, "test")
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("output = %q, want the literal {} Cursor requires on every code path", out.String())
	}
}

func TestRunCursorPrintsEmptyObjectOnUnparseableInput(t *testing.T) {
	var out bytes.Buffer
	RunCursor(nil, strings.NewReader("not json"), &out, "test")
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("output = %q, want {}", out.String())
	}
}

func TestRunCopilotVsCodeShapeRewritesAMatchedCommand(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)
	var out bytes.Buffer
	RunCopilot(nil, in, &out, "test")
	got := out.String()
	if !strings.Contains(got, `"updatedInput"`) || !strings.Contains(got, "sctx git status") {
		t.Errorf("output = %q, want hookSpecificOutput.updatedInput carrying the rewritten command", got)
	}
}

func TestRunCopilotCliShapePreservesOtherArgsAndRewrites(t *testing.T) {
	// toolArgs is a JSON-ENCODED STRING per GitHub's docs, not a nested object.
	in := strings.NewReader(`{"toolName":"bash","toolArgs":"{\"command\":\"git status\",\"description\":\"check status\"}"}`)
	var out bytes.Buffer
	RunCopilot(nil, in, &out, "test")
	got := out.String()
	if !strings.Contains(got, `"modifiedArgs"`) {
		t.Fatalf("output = %q, want modifiedArgs", got)
	}
	if !strings.Contains(got, "sctx git status") {
		t.Errorf("output = %q, want the rewritten command", got)
	}
	if !strings.Contains(got, "check status") {
		t.Errorf("output = %q, want the untouched description preserved", got)
	}
}

func TestRunCopilotPrintsNothingOnAMiss(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"echo hi"}}`)
	var out bytes.Buffer
	RunCopilot(nil, in, &out, "test")
	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing (fail open)", out.String())
	}
}

func TestRunCopilotIgnoresANonShellTool(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"ReadFile","tool_input":{"path":"x"}}`)
	var out bytes.Buffer
	RunCopilot(nil, in, &out, "test")
	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing for a non-shell tool", out.String())
	}
}

func TestRunDroidRewritesAnExecuteCommand(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Execute","tool_input":{"command":"git status"}}`)
	var out bytes.Buffer
	RunDroid(nil, in, &out, "test")
	got := out.String()
	if !strings.Contains(got, `"updatedInput"`) || !strings.Contains(got, "sctx git status") {
		t.Errorf("output = %q, want hookSpecificOutput.updatedInput carrying the rewritten command", got)
	}
	if strings.Contains(got, "permissionDecision\":") && !strings.Contains(got, "permissionDecisionReason") {
		t.Errorf("output = %q, must not set permissionDecision — Droid's own flow owns the verdict", got)
	}
}

func TestRunDroidStepsAsideOnADenylistedCommand(t *testing.T) {
	patterns := []string{"git push"}
	if !droidDenylisted("git push origin main", patterns) {
		t.Fatal("expected a denylist match")
	}
	if droidDenylisted("git status", patterns) {
		t.Fatal("expected no denylist match for an unrelated command")
	}
}

func TestRunDroidWildcardDenylistPatternMatches(t *testing.T) {
	if !droidDenylisted("curl https://example.com", []string{"curl:*"}) {
		t.Fatal("expected curl:* to match a curl invocation")
	}
	if !droidDenylisted("docker run --rm x", []string{"docker *"}) {
		t.Fatal("expected 'docker *' to match a docker invocation")
	}
}

func TestRunDroidIgnoresANonExecuteTool(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"ReadFile","tool_input":{"path":"x"}}`)
	var out bytes.Buffer
	RunDroid(nil, in, &out, "test")
	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing for a non-Execute tool", out.String())
	}
}
