// This file implements `sctx hook claude`, the Claude Code PreToolUse Bash
// hook: it reads a tool-call JSON payload on stdin and, when the command
// matches a known rewrite rule (see rewrite.go), prints an "updatedInput"
// JSON directive on stdout that prefixes the command with "sctx ". It is
// fail-open by design — any parse error, unmatched command, or non-Bash tool
// call prints nothing and exits 0, so it can never break the agent's tool
// call.
//
// When no rule matches, the command is left exactly as written and the miss is
// recorded as a coverage-gap event — the meter that says which formatter is
// worth building next.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/synapctx/sctx/internal/adapters/format/projectfilter"
	"github.com/synapctx/sctx/internal/platform/progkey"

	"github.com/cloudresty/ulid"

	"github.com/synapctx/sctx/internal/adapters/telemetry/spool"
	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
)

// toolCall is decoded leniently: unknown/extra fields are ignored, and
// tool_input is left as a map so we only need the "command" key.
type toolCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// RunClaude implements `sctx hook claude`. args is everything after "claude" on
// the command line. version is sctx's own build version, recorded on any
// coverage-gap event this run spools.
func RunClaude(args []string, in io.Reader, out io.Writer, version string) int {
	return runClaude(args, in, out, version)
}

func runClaude(_ []string, in io.Reader, out io.Writer, version string) int {
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}

	var call toolCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}
	if call.ToolName != "Bash" {
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

	if os.Getenv("SCT__REWRITE_DISABLED") == "true" {
		return 0
	}

	root, matchers := trustedProjectMatchers()
	if rewritten, ok := rewriteWithProject(cmd, root, matchers); ok {
		writeRewrite(out, rewritten)
		return 0
	}

	// No rule matched: leave the command exactly as the agent wrote it, and
	// record the miss. gapSegment is the filter that keeps this meter readable —
	// it refuses an already-wrapped command, a command whose head we could not
	// identify, and shell builtins with no output worth compressing.
	if seg, ok := gapSegment(cmd); ok {
		spoolCoverageGap(seg, version)
	} else if seg, reason, ok := declineSegment(cmd); ok {
		spoolCoverageDecline(seg, reason, version)
	}
	return 0
}

func spoolCoverageDecline(segText, reason, version string) {
	spoolImprovementEvent(telemetry.KindCoverageDecline, segText, reason, version)
}

func trustedProjectMatchers() (string, []projectfilter.Matcher) {
	wd, err := os.Getwd()
	if err != nil {
		return "", nil
	}
	trustPath, err := projectfilter.DefaultTrustPath()
	if err != nil {
		return "", nil
	}
	loaded, trusted, err := projectfilter.LoadTrustedFrom(wd, trustPath)
	if err != nil || !trusted {
		return "", nil
	}
	return loaded.Root, loaded.Matchers()
}

// spoolCoverageGap records a command sctx does not rewrite.
//
// The MEANING of this event changed when the fallback was removed. It used to
// mean "another wrapper covered this and we did not", so it was recorded only
// after that wrapper confirmed a rewrite, and the dashboard read it as a
// retirement meter. It now means "sctx saw a command it has no formatter for" —
// a broader set, and the more useful one, because it no longer depends on
// whether some other tool happened to be installed.
//
// Entirely best-effort: any failure is swallowed so the hook's output and exit
// behaviour are never affected.
// segText is a single clean segment (from gapSegment), not the full raw
// command line — it may still be a compound command's individual piece
// (e.g. "npm test" out of "cd sub && npm test").
func spoolCoverageGap(segText, version string) {
	spoolImprovementEvent(telemetry.KindCoverageGap, segText, coverageGapReason(segText), version)
}

func spoolImprovementEvent(kind, segText, reason, version string) {
	// Consent gates COLLECTION, not just delivery. Writing a refused customer's
	// commands to a local spool that a later `sctx flush` would drain is the same
	// data leaving by a slower route — and the file itself is a record they did
	// not agree to. Fails closed: no decision, no write.
	//
	// A coverage gap is IMPROVEMENT data — it ranks what we build next and tells
	// the customer nothing they did not already know from running the command.
	// Their own savings report is a different purpose and is not gated here.
	if !config.TelemetryPermitted(telemetry.PurposeImprovement) {
		return
	}
	id, err := ulid.New()
	if err != nil {
		return
	}
	dir := os.Getenv("SCT__SPOOL_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".config", "sctx", "spool")
	}
	var repoName string
	if wd, err := os.Getwd(); err == nil {
		repoName = gitrepo.Detect(wd)
	}
	program := deriveProgram(segText)
	_ = spool.Append(dir, telemetry.Event{
		ID:             id,
		Kind:           kind,
		Tool:           "sctx",
		Version:        version,
		RepositoryName: repoName,
		// Command is set to Program, never the full command line: args may
		// contain secrets and only the program/subcommand key is needed for
		// coverage-gap aggregation.
		Command:       program,
		Program:       program,
		DeclineReason: reason,
		At:            time.Now().UTC(),
	})
}

func coverageGapReason(command string) string {
	tokens := tokenize(command)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) {
		return telemetry.DeclineUnsupportedCommand
	}
	program := filepathBase(shellTokenValue(tokens[idx].text))
	if _, known := subcommandTable[program]; known {
		return telemetry.DeclineUnsupportedSubcommand
	}
	return telemetry.DeclineUnsupportedCommand
}

// deriveProgram returns the same "program + first subcommand" key used by
// exec-savings telemetry, computed from the raw command string using the
// rewrite tokenizer's own tokenization/assignment-skipping so the two never
// drift apart.
func deriveProgram(cmd string) string {
	tokens := tokenize(cmd)
	idx := 0
	for idx < len(tokens) && isAssignment(tokens[idx].text) {
		idx++
	}
	if idx >= len(tokens) {
		return ""
	}
	argv := make([]string, 0, len(tokens)-idx)
	for _, token := range tokens[idx:] {
		argv = append(argv, token.text)
	}
	return progkey.FromArgv(argv)
}

func writeRewrite(out io.Writer, rewritten string) {
	output := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecisionReason": "sctx auto-rewrite",
			"updatedInput": map[string]any{
				"command": rewritten,
			},
		},
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return
	}
	fmt.Fprintln(out, string(encoded))
}
