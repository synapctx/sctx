package agentsetup

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

// Hook wiring for Claude Code.
//
// `sctx setup` checked instruction files and not the hooks, which meant it could
// report a green install on a machine where sctx never ran at all. A wrapper prints
// `[warn] No hook installed` for exactly this reason and it is the right
// instinct: the two halves fail independently and look identical from outside.
//
// Four hooks, earning their place differently:
//
//   - **PreToolUse on Bash** rewrites covered commands to `sctx <cmd>`. This is
//     the savings engine; without it sctx compresses nothing.
//   - **SessionStart** briefs the agent before it has read anything: the org
//     memory bound to this repository, how fresh the index is against local
//     HEAD, and the tools to open with — named in THIS machine's MCP namespace,
//     which no shipped document can know.
//   - **PreToolUse on Grep|Glob|Agent** catches the decay of that brief. An
//     agent about to grep has just decided it has a "where is X" question, which
//     is the question the org-wide graph answers better. It speaks twice per
//     session and then stops; a nudge on every search is a tax on the tool the
//     developer chose.
//   - **PostToolUse on Edit/Write/Bash** delivers what nobody asked for: org
//     memory about a file being edited, and — after a grep for an identifier —
//     the call sites in OTHER repositories the search structurally could not
//     see. This is the delivery half of decision 0006. Recall cannot be earned
//     the way search can, because no agent thinks to ask for it, and a grep has
//     already answered the local question correctly.
//
// `Bash` was held out of this matcher until 2026-08-02 and the reasons are worth
// keeping: resolving a grep pattern to a symbol path cost a ~1.1s semantic
// retrieval (now a ~5ms term lookup), and the graph held no cross-repository
// call edges at all, so the nudge would have added a second to every grep to
// say nothing. Both are fixed; if either regresses, take Bash back out rather
// than shipping a slow tax on a tool sold for making commands cheaper.
//
// Everything here is conservative about a file we do not own. `settings.json`
// holds the developer's own hooks, permissions and plugin state; we add entries
// and never remove, reorder or rewrite anything else.

// HookSpec is one hook we manage.
type HookSpec struct {
	Event   string // "PreToolUse" | "PostToolUse" | "SessionStart" (Gemini: "BeforeTool")
	Matcher string // tool-name regex, e.g. "Bash" or "Edit|Write"
	Purpose string
	// Subcommand is the `sctx hook <x>` verb. Detection matches on THIS plus the
	// program name, never on Command's absolute path — see invokesSctxHook.
	Subcommand string
	// Command is what we write when the hook is absent.
	Command string
	// Name and Timeout are written when the client documents them. Gemini CLI
	// does (a friendly identifier and a millisecond limit); Claude Code does not,
	// and writing fields a client has never heard of into its settings is how a
	// validated config file starts rejecting the whole entry.
	Name    string
	Timeout int
}

// GeminiHooks is the auto-wrap hook for Gemini CLI.
//
// `BeforeTool` is Gemini's PreToolUse: its `hookSpecificOutput.tool_input`
// merges over the model's arguments before the tool runs, which is the same
// rewrite Claude's `updatedInput` performs. The matcher names the shell tool
// specifically — a `*` matcher would run sctx before every file read the agent
// performs, to decline each time.
//
// There is no Gemini equivalent of the memory hook here: PostToolUse surfacing
// is a separate feature and is not implemented for this client.
func GeminiHooks(binary string) []HookSpec {
	return []HookSpec{
		{
			Event:      "BeforeTool",
			Matcher:    "run_shell_command",
			Purpose:    "rewrites covered commands to sctx — this is what produces the savings",
			Subcommand: "gemini",
			Command:    binary + " hook gemini",
			Name:       "sctx",
			Timeout:    5000,
		},
	}
}

// hooksFor is the hook set for one agent, empty when sctx installs none.
func hooksFor(a Agent, binary string) []HookSpec {
	switch a.ID {
	case "claude":
		return ClaudeHooks(binary)
	case "gemini":
		return GeminiHooks(binary)
	}
	return nil
}

// hookSettingsPath is where that agent keeps them. Both clients happen to use
// `settings.json`; nothing depends on that staying true.
func hookSettingsPath(home string, a Agent) string {
	switch a.ID {
	case "claude":
		return filepath.Join(home, ".claude", "settings.json")
	case "gemini":
		return filepath.Join(home, ".gemini", "settings.json")
	}
	return ""
}

// ClaudeHooks are the hooks `sctx setup` installs for Claude Code.
func ClaudeHooks(binary string) []HookSpec {
	return []HookSpec{
		{
			Event:      "PreToolUse",
			Matcher:    "Bash",
			Purpose:    "rewrites covered commands to sctx — this is what produces the savings",
			Subcommand: "claude",
			Command:    binary + " hook claude",
		},
		{
			Event:      "PostToolUse",
			Matcher:    "Edit|Write|Bash",
			Purpose:    "surfaces org memory for files you edit, and cross-repo call sites grep cannot see",
			Subcommand: "claude-post-tool",
			Command:    binary + " hook claude-post-tool",
		},
		{
			Event: "SessionStart",
			// Every source, including `compact`: a compaction is the moment the
			// startup brief was just dropped from context, so it is exactly when
			// re-stating it is worth the two lines it costs.
			Matcher:    "startup|resume|clear|compact",
			Purpose:    "briefs the agent at session start: org memory bound to this repository, index freshness, the tools to open with",
			Subcommand: "claude-session-start",
			Command:    binary + " hook claude-session-start",
		},
		{
			// A SECOND PreToolUse group, not an extension of the Bash matcher.
			// They are different hooks with different failure modes: the Bash one
			// rewrites a command and must never be slowed, this one only ever adds
			// a sentence. Detection is per (event, matcher, subcommand), so the
			// two groups coexist without either satisfying the other.
			Event:      "PreToolUse",
			Matcher:    "Grep|Glob|Agent",
			Purpose:    "on the first local searches of a session, points at the org-wide graph and memory",
			Subcommand: "claude-first-search",
			Command:    binary + " hook claude-first-search",
		},
	}
}

// HookState is what we found for one spec.
type HookState struct {
	HookSpec
	Installed bool
}

// InspectHooks reports which of our hooks are wired into Claude Code's settings.
// Kept under its original name because callers and tests use it.
func InspectHooks(home, binary string) (settingsPath string, states []HookState, err error) {
	return InspectAgentHooks(home, claudeAgent(), binary)
}

// InstallHooks adds any missing Claude Code hook entry.
func InstallHooks(home, binary string) ([]string, error) {
	return InstallAgentHooks(home, claudeAgent(), binary)
}

func claudeAgent() Agent {
	a, _ := agentdoc.AgentByID("claude")
	return a
}

// InspectAgentHooks reports which of our hooks are wired into one agent's
// settings. An agent sctx installs no hooks for returns nothing, not an error.
func InspectAgentHooks(home string, a Agent, binary string) (settingsPath string, states []HookState, err error) {
	specs := hooksFor(a, binary)
	settingsPath = hookSettingsPath(home, a)
	if len(specs) == 0 || settingsPath == "" {
		return settingsPath, nil, nil
	}
	raw, readErr := os.ReadFile(settingsPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return settingsPath, nil, fmt.Errorf("reading %s: %w", settingsPath, readErr)
	}
	var doc map[string]any
	if len(raw) > 0 {
		if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil {
			// A settings file we cannot parse is one we must not write. Report
			// every hook as missing and let the caller say so — silently
			// rewriting a developer's malformed JSON would lose whatever they
			// were in the middle of.
			return settingsPath, nil, fmt.Errorf("parsing %s: %w", settingsPath, jsonErr)
		}
	}
	for _, spec := range specs {
		states = append(states, HookState{HookSpec: spec, Installed: hookPresent(doc, spec)})
	}
	return settingsPath, states, nil
}

// InstallAgentHooks adds any missing hook entry, preserving everything else.
func InstallAgentHooks(home string, a Agent, binary string) ([]string, error) {
	settingsPath, states, err := InspectAgentHooks(home, a, binary)
	if err != nil {
		return nil, err
	}
	if settingsPath == "" || len(states) == 0 {
		return nil, nil
	}
	missing := make([]HookSpec, 0, len(states))
	for _, st := range states {
		if !st.Installed {
			missing = append(missing, st.HookSpec)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}

	raw, readErr := os.ReadFile(settingsPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("reading %s: %w", settingsPath, readErr)
	}
	doc := map[string]any{}
	if len(raw) > 0 {
		if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil {
			return nil, fmt.Errorf("parsing %s: %w", settingsPath, jsonErr)
		}
	}

	var changed []string
	for _, spec := range missing {
		addHook(doc, spec)
		changed = append(changed, fmt.Sprintf("hooked %s(%s) — %s", spec.Event, spec.Matcher, spec.Purpose))
	}
	if n := dropRemovedFlags(doc); n > 0 {
		changed = append(changed, fmt.Sprintf("removed %d stale --fallback flag(s) from existing sctx hooks", n))
	}

	out, err := json.Marshal(doc, jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", settingsPath, err)
	}
	return changed, nil
}

// hookPresent reports whether one of OUR hooks is already registered for this
// event and matcher.
//
// Matching is on the sctx INVOCATION, never on the absolute path. The path in an
// existing entry is whatever sctx was when the developer first ran setup — a
// Homebrew install, a dev build, a since-moved binary — and comparing it against
// the currently-running executable reports "not installed" and appends a SECOND
// hook. Verified end to end: an entry reading `/x/sctx hook claude --verbose`
// was missed by a path-based comparison and every Bash command would then
// have been wrapped twice.
//
// It also cannot be a substring test, because `sctx hook claude` is a prefix of
// `sctx hook claude-post-tool`: the memory hook would satisfy the Bash hook and
// the savings engine would never be installed.
func hookPresent(doc map[string]any, spec HookSpec) bool {
	// Split rather than chained: a single-value type assertion PANICS on a miss,
	// and a machine with no settings.json at all is the normal fresh-install
	// case, not an edge case.
	hooks, _ := doc["hooks"].(map[string]any)
	groups, _ := hooks[spec.Event].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		if m, _ := group["matcher"].(string); m != spec.Matcher {
			continue
		}
		entries, _ := group["hooks"].([]any)
		for _, e := range entries {
			entry, _ := e.(map[string]any)
			cmd, _ := entry["command"].(string)
			if invokesSctxHook(cmd, spec.Subcommand) {
				return true
			}
		}
	}
	return false
}

// invokesSctxHook reports whether cmd runs `<anything>/sctx hook <subcommand>`.
//
// Whole-token comparison on each field: the program token must END in "sctx"
// (after a path separator, so "mysctx" does not match) and the two tokens after
// it must be exactly "hook" and the subcommand. Trailing flags are the
// developer's and are ignored.
func invokesSctxHook(cmd, subcommand string) bool {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		prog := f
		if idx := strings.LastIndexAny(prog, `/\`); idx >= 0 {
			prog = prog[idx+1:]
		}
		if prog != "sctx" && prog != "sctx.exe" {
			continue
		}
		if i+2 < len(fields) && fields[i+1] == "hook" && fields[i+2] == subcommand {
			return true
		}
	}
	return false
}

// addHook appends our entry, reusing an existing group for the same matcher
// rather than creating a second one — two groups with the same matcher both
// fire, which is legal and confusing.
func addHook(doc map[string]any, spec HookSpec) {
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	groups, _ := hooks[spec.Event].([]any)
	entry := map[string]any{"type": "command", "command": spec.Command}
	if spec.Name != "" {
		entry["name"] = spec.Name
	}
	if spec.Timeout > 0 {
		entry["timeout"] = spec.Timeout
	}

	for _, g := range groups {
		group, _ := g.(map[string]any)
		if m, _ := group["matcher"].(string); m == spec.Matcher {
			entries, _ := group["hooks"].([]any)
			group["hooks"] = append(entries, entry)
			hooks[spec.Event] = groups
			return
		}
	}
	hooks[spec.Event] = append(groups, map[string]any{
		"matcher": spec.Matcher,
		"hooks":   []any{entry},
	})
}

// removedHookFlags are flags sctx once accepted and no longer does. They are
// stripped from OUR entries on reinstall.
//
// The flag is already inert — `sctx hook claude` ignores its arguments — so this
// is not a correctness fix. It is that a settings file naming a tool we removed
// tells the next reader, human or agent, that sctx still depends on it. An
// install that leaves its own obsolete configuration behind is how a migration
// stays half-done forever.
//
// Only entries that are OURS are touched: another tool's hook is none of our
// business, and a `--fallback` on someone else's command may still be load-bearing.
var removedHookFlags = []string{"--fallback"}

func dropRemovedFlags(doc map[string]any) int {
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		return 0
	}
	cleaned := 0
	for _, groups := range hooks {
		list, _ := groups.([]any)
		for _, g := range list {
			group, _ := g.(map[string]any)
			entries, _ := group["hooks"].([]any)
			for _, e := range entries {
				entry, _ := e.(map[string]any)
				cmd, _ := entry["command"].(string)
				if cmd == "" || !invokesSctxHook(cmd, "claude") {
					continue
				}
				if stripped := withoutFlags(cmd, removedHookFlags); stripped != cmd {
					entry["command"] = stripped
					cleaned++
				}
			}
		}
	}
	return cleaned
}

// withoutFlags removes each named flag and, when written separately, its value.
// `--flag=value` is one token; `--flag value` is two.
func withoutFlags(cmd string, flags []string) string {
	fields := strings.Fields(cmd)
	out := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		skip := false
		for _, flag := range flags {
			if fields[i] == flag {
				i++ // also drop its value
				skip = true
				break
			}
			if strings.HasPrefix(fields[i], flag+"=") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, fields[i])
		}
	}
	return strings.Join(out, " ")
}
