package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/telemetry"
)

// readSpoolEvents decodes every JSONL line in dir's pending spool file, or
// returns nil if the file doesn't exist.
func readSpoolEvents(t *testing.T, dir string) []telemetry.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "pending.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading spool: %v", err)
	}
	var events []telemetry.Event
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var ev telemetry.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("decoding spooled event: %v", err)
		}
		events = append(events, ev)
	}
	return events
}

func TestRunClaudeNoFallback(t *testing.T) {
	tests := []struct {
		name      string
		stdin     string
		wantEmpty bool
		wantCmd   string
	}{
		{
			name:    "bash command rewritten",
			stdin:   `{"tool_name":"Bash","tool_input":{"command":"git status"}}`,
			wantCmd: "sctx git status",
		},
		{
			name:      "non-bash tool ignored",
			stdin:     `{"tool_name":"Read","tool_input":{"file_path":"x"}}`,
			wantEmpty: true,
		},
		{
			name:      "invalid json ignored",
			stdin:     `not json`,
			wantEmpty: true,
		},
		{
			name:      "empty command ignored",
			stdin:     `{"tool_name":"Bash","tool_input":{"command":""}}`,
			wantEmpty: true,
		},
		{
			name:      "no rewrite rule applies",
			stdin:     `{"tool_name":"Bash","tool_input":{"command":"mix test"}}`,
			wantEmpty: true,
		},
		{
			name:    "extra unknown fields tolerated",
			stdin:   `{"tool_name":"Bash","session_id":"abc","tool_input":{"command":"grep foo .","other":123}}`,
			wantCmd: "sctx grep foo .",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			code := runClaude(nil, strings.NewReader(tt.stdin), &out, "v-test")
			if code != 0 {
				t.Fatalf("runClaude() exit code = %d, want 0", code)
			}
			if tt.wantEmpty {
				if out.Len() != 0 {
					t.Fatalf("runClaude() output = %q, want empty", out.String())
				}
				return
			}
			var decoded struct {
				HookSpecificOutput struct {
					HookEventName            string `json:"hookEventName"`
					PermissionDecisionReason string `json:"permissionDecisionReason"`
					UpdatedInput             struct {
						Command string `json:"command"`
					} `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
				t.Fatalf("unmarshal output: %v (output=%q)", err, out.String())
			}
			if decoded.HookSpecificOutput.HookEventName != "PreToolUse" {
				t.Errorf("hookEventName = %q, want PreToolUse", decoded.HookSpecificOutput.HookEventName)
			}
			if decoded.HookSpecificOutput.UpdatedInput.Command != tt.wantCmd {
				t.Errorf("updatedInput.command = %q, want %q", decoded.HookSpecificOutput.UpdatedInput.Command, tt.wantCmd)
			}
		})
	}
}

// A command sctx has no rule for is recorded, so the gap meter shows which
// formatter is worth building next. This used to fire only when ANOTHER wrapper
// confirmed a rewrite, which made the meter depend on what else was installed.
func TestAnUnmatchedCommandIsRecordedAsACoverageGap(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"mix test"}}`
	var out bytes.Buffer
	if code := runClaude(nil, strings.NewReader(stdin), &out, "v-test"); code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	// The command is left exactly as written: sctx has nothing to offer it.
	if out.Len() != 0 {
		t.Fatalf("rewrote a command it has no rule for: %q", out.String())
	}

	events := readSpoolEvents(t, spoolDir)
	if len(events) != 1 {
		t.Fatalf("spooled events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != telemetry.KindCoverageGap {
		t.Errorf("Kind = %q, want %q", ev.Kind, telemetry.KindCoverageGap)
	}
	if ev.Program != "mix test" {
		t.Errorf("Program = %q, want %q", ev.Program, "mix test")
	}
	// Arguments must never reach telemetry — only the program key.
	if ev.Command != ev.Program {
		t.Errorf("Command = %q, want it to equal Program %q", ev.Command, ev.Program)
	}
	if ev.Version != "v-test" || ev.ID == "" {
		t.Errorf("Version = %q, ID = %q", ev.Version, ev.ID)
	}
}

// A command sctx DOES cover is not a gap.
func TestACoveredCommandIsNotACoverageGap(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"git status"}}`
	var out bytes.Buffer
	if code := runClaude(nil, strings.NewReader(stdin), &out, "v-test"); code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	if events := readSpoolEvents(t, spoolDir); len(events) != 0 {
		t.Fatalf("spooled events = %d, want 0", len(events))
	}
}

// Shell builtins and already-wrapped commands must never reach the meter, or
// the ranking that decides what to build next is dominated by `cd`.
func TestNoiseIsNotRecordedAsACoverageGap(t *testing.T) {
	for _, cmd := range []string{"cd /tmp", "echo hello", "sctx mix test"} {
		spoolDir := t.TempDir()
		t.Setenv("SCT__SPOOL_DIR", spoolDir)
		stdin := `{"tool_name":"Bash","tool_input":{"command":"` + cmd + `"}}`
		var out bytes.Buffer
		runClaude(nil, strings.NewReader(stdin), &out, "v-test")
		if events := readSpoolEvents(t, spoolDir); len(events) != 0 {
			t.Errorf("%q was recorded as a coverage gap: %+v", cmd, events)
		}
	}
}

// Telemetry is best-effort and must never affect the hook. An unwritable spool
// is the cheapest way to prove it: the hook still exits 0 and still declines to
// rewrite, rather than erroring on a command it merely failed to MEASURE.
func TestASpoolFailureNeverAffectsTheHook(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing blocker file: %v", err)
	}
	// A regular file where a spool path component is expected makes
	// os.MkdirAll fail inside spool.Append.
	t.Setenv("SCT__SPOOL_DIR", filepath.Join(blocker, "spool"))

	stdin := `{"tool_name":"Bash","tool_input":{"command":"mix test"}}`
	var out bytes.Buffer
	if code := runClaude(nil, strings.NewReader(stdin), &out, "v-test"); code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

// A gap inside a pipeline is still a gap: the meter must see `mix test`, not
// nothing, or every pipelined command is invisible to it.
func TestAGapInsideAPipelineIsStillRecorded(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"mix test | tail -5"}}`
	var out bytes.Buffer
	runClaude(nil, strings.NewReader(stdin), &out, "v-test")

	events := readSpoolEvents(t, spoolDir)
	if len(events) != 1 || events[0].Program != "mix test" {
		t.Fatalf("events = %+v, want one recording \"mix test\"", events)
	}
}

func TestAGapAfterACdIsStillRecorded(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"cd sub && mix test"}}`

	var out bytes.Buffer
	code := runClaude(nil, strings.NewReader(stdin), &out, "v-test")
	if code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}

	events := readSpoolEvents(t, spoolDir)
	if len(events) != 1 {
		t.Fatalf("spooled events = %d, want 1", len(events))
	}
	if events[0].Program != "mix test" {
		t.Errorf("Program = %q, want %q", events[0].Program, "mix test")
	}
}

func TestAnUnsafeDownstreamStageIsNotRecorded(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"go test ./... | grep FAIL"}}`

	var out bytes.Buffer
	code := runClaude(nil, strings.NewReader(stdin), &out, "v-test")
	if code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	if events := readSpoolEvents(t, spoolDir); len(events) != 0 {
		t.Fatalf("spooled events = %d, want 0", len(events))
	}
}

func TestCommandSubstitutionIsNotRecorded(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"mix test $(x)"}}`

	var out bytes.Buffer
	code := runClaude(nil, strings.NewReader(stdin), &out, "v-test")
	if code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	if events := readSpoolEvents(t, spoolDir); len(events) != 0 {
		t.Fatalf("spooled events = %d, want 0", len(events))
	}
}

func TestRunClaudeRewritesSegmentInPipeNoFallback(t *testing.T) {
	stdin := `{"tool_name":"Bash","tool_input":{"command":"go test ./... 2>&1 | tail -50"}}`
	var out bytes.Buffer
	code := runClaude(nil, strings.NewReader(stdin), &out, "v-test")
	if code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	var decoded struct {
		HookSpecificOutput struct {
			UpdatedInput struct {
				Command string `json:"command"`
			} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal output: %v (output=%q)", err, out.String())
	}
	want := "sctx go test ./... 2>&1 | tail -50"
	if decoded.HookSpecificOutput.UpdatedInput.Command != want {
		t.Errorf("updatedInput.command = %q, want %q", decoded.HookSpecificOutput.UpdatedInput.Command, want)
	}
}

func TestDeriveProgram(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"go test ./...", "go test"},
		{"ls -la", "ls"},
		{"terraform plan -out x", "terraform plan"},
		{"mix test 2>&1", "mix test"},
		{"git --no-pager -C repo -c color.ui=false status --short", "git status"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := deriveProgram(tt.cmd); got != tt.want {
				t.Errorf("deriveProgram(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

// A command whose PROGRAM token cannot be located must not reach the meter.
//
// `A=" go test "` is an assignment, not an invocation — the only "program" in it
// lives inside a quoted string. Recording a gap here would attribute one to
// `go test`, a command the developer never ran, and the meter decides which
// formatter gets built next. sctx's own scanner was fixed for exactly this
// corruption; the measurement must not reintroduce it.
func TestACommandWithNoProgramTokenIsNotRecorded(t *testing.T) {
	spoolDir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", spoolDir)

	stdin := `{"tool_name":"Bash","tool_input":{"command":"A=\" go test \""}}`
	var out bytes.Buffer
	if code := runClaude(nil, strings.NewReader(stdin), &out, "v-test"); code != 0 {
		t.Fatalf("runClaude() exit code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Errorf("runClaude() output = %q, want empty (passthrough)", out.String())
	}
	if events := readSpoolEvents(t, spoolDir); len(events) != 0 {
		t.Errorf("recorded a gap for a command with no program token: %+v", events)
	}
}

func TestMeasuredNoFormatterCandidateIsNotRecordedAgain(t *testing.T) {
	for _, command := range []string{"go doc ./internal/application/run", "go -C repo doc ./pkg", "swag init -g main.go"} {
		if segment, ok := gapSegment(command); ok {
			t.Fatalf("gapSegment(%q) = %q; deliberately rejected candidate must not keep polluting ranking", command, segment)
		}
	}
}

// uncoveredFixtures are commands sctx does NOT cover, used by the two tests
// below. They prove nothing the moment sctx starts covering them, and that has
// now happened TWICE — first when `ssh` gained a formatter, then when `cargo`,
// `terraform` and `helm` were added to the rewrite table. Declared once so the
// pair cannot drift apart, and guarded by TestGapFixturesAreActuallyUncovered so
// the next such change fails there, with an explanation, rather than looking
// like a regression in gap recording.
//
// Pick replacements that are COHERENT programs (something we could plausibly
// write a formatter for) whose first argument is an operation, or the fixtures
// stop resembling the real gaps they stand in for.
var uncoveredFixtures = []string{
	"mix test",
	"vault read secret/db",
	"wget https://example.com/x.tar.gz",
}

// The counterweight to the test above: the no-program-token guard must not turn
// into a blanket refusal to record, or the gap meter goes blind and we build
// formatters by guesswork.
func TestRealCoverageGapsAreStillRecorded(t *testing.T) {
	for _, cmd := range uncoveredFixtures {
		spoolDir := t.TempDir()
		t.Setenv("SCT__SPOOL_DIR", spoolDir)
		stdin := `{"tool_name":"Bash","tool_input":{"command":` + strconv.Quote(cmd) + `}}`
		var out bytes.Buffer
		runClaude(nil, strings.NewReader(stdin), &out, "v-test")
		if events := readSpoolEvents(t, spoolDir); len(events) != 1 {
			t.Errorf("%q recorded %d gap events, want 1 — this IS a coverage gap, not an unparseable command", cmd, len(events))
		}
	}
}

// TestDelegationFixturesAreActuallyUncovered guards the test above from rotting. Its
// fixtures only prove anything if sctx does not cover them, and that stopped being true for
// `ssh` the moment ssh gained a formatter — the test then failed for the right reason and
// had to be re-pointed. This checks the premise directly so the next such change fails HERE,
// with an explanation, instead of looking like a regression.
func TestGapFixturesAreActuallyUncovered(t *testing.T) {
	for _, cmd := range uncoveredFixtures {
		if _, covered := matchSegment(cmd); covered {
			t.Errorf("%q is now COVERED by sctx, so it is no longer a coverage gap and cannot test gap recording; pick another program for TestRealCoverageGapsAreStillRecorded", cmd)
		}
	}
}
