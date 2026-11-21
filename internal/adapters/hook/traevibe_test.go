package hook

import (
	"bytes"
	json "encoding/json/v2"
	"strings"
	"testing"
)

func TestRunTrae(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantOut bool
	}{
		{"rewrites RunCommand and keeps description", `{"tool_name":"RunCommand","tool_input":{"command":"go test ./...","description":"run tests"}}`, true},
		{"other tool untouched", `{"tool_name":"ReadFile","tool_input":{"command":"go test ./..."}}`, false},
		{"uncovered command untouched", `{"tool_name":"RunCommand","tool_input":{"command":"echo hi"}}`, false},
		{"malformed json is fail-open", `{nope`, false},
		{"missing command", `{"tool_name":"RunCommand","tool_input":{}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := RunTrae(nil, strings.NewReader(tc.in), &out, "test"); code != 0 {
				t.Fatalf("exit %d", code)
			}
			if (out.Len() > 0) != tc.wantOut {
				t.Fatalf("output = %q", out.String())
			}
			if !tc.wantOut {
				return
			}
			var got struct {
				Hook struct {
					Event string         `json:"hookEventName"`
					Input map[string]any `json:"updatedInput"`
					Perm  any            `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Hook.Event != "PreToolUse" || got.Hook.Perm != nil {
				t.Fatalf("envelope = %+v", got.Hook)
			}
			if c, _ := got.Hook.Input["command"].(string); c != "sctx go test ./..." {
				t.Fatalf("command = %q", c)
			}
			if got.Hook.Input["description"] != "run tests" {
				t.Fatalf("description lost: %v", got.Hook.Input)
			}
		})
	}
}

func TestRunVibe(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantOut bool
	}{
		{"bash", `{"tool_name":"bash","tool_input":{"command":"go vet ./..."},"hook_event_name":"pre_tool"}`, true},
		{"unified harness spelling", `{"tool_name":"file_system.bash","tool_input":{"command":"go vet ./..."}}`, true},
		{"other tool", `{"tool_name":"read_file","tool_input":{"command":"go vet ./..."}}`, false},
		{"no opinion is empty stdout", `{"tool_name":"bash","tool_input":{"command":"echo hi"}}`, false},
		{"malformed", `[`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			RunVibe(nil, strings.NewReader(tc.in), &out, "test")
			if (out.Len() > 0) != tc.wantOut {
				t.Fatalf("output = %q", out.String())
			}
			if tc.wantOut && !strings.Contains(out.String(), `"hook_specific_output":{"tool_input":{"command":"sctx go vet ./..."}}`) {
				t.Fatalf("envelope = %s", out.String())
			}
		})
	}
}
