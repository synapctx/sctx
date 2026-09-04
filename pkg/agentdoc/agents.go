package agentdoc

// The agents we know how to teach.
//
// This table is the whole of our per-agent knowledge, and it is deliberately
// data rather than code so adding a tool is one row. Three properties keep it
// safe to be incomplete or slightly wrong, which it certainly is — instruction
// formats in this space change faster than any release cycle:
//
//  1. **Detection is by existence, never by assumption.** We write only where an
//     agent has already left its own configuration. A customer using Codex must
//     never end up with a `~/.claude/CLAUDE.md` they did not ask for, and a row
//     with a stale or wrong path simply never matches — a bad guess is inert,
//     not destructive.
//  2. **Nothing is created speculatively.** If no agent is detected we say what
//     we looked for and stop, rather than defaulting to the most popular one.
//  3. **Include support is opt-IN.** `@file` includes are a Claude Code feature;
//     assuming another tool has them writes a line that silently loads nothing.
//     Anything we are not certain about gets the inline block, which works
//     everywhere because it is just text in the file the agent already reads.
//
// A fourth rule was added on 2026-08-18, after the first end-to-end audit of the
// non-Claude rows: **a row states only capabilities that were VERIFIED against
// the agent's own shipped binary or documentation.** The audit found the Kilo
// row pointing at a path that release had deprecated, and every agent being told
// a hook would rewrite its commands when only Claude Code has one — so five of
// seven agents were instructed never to type the thing they had to type. A
// capability we have not verified is left at its zero value, which means "sctx
// does not do this here" and is reported as such, rather than being assumed.
type Agent struct {
	ID   string // stable identifier, also the --agent flag value and the ?agent= value
	Name string // for humans

	// Root is the instruction file, relative to the home directory. This is the
	// file the agent loads by convention; we append a delimited block to it.
	Root string

	// Detect are paths, relative to home, whose existence means this agent is
	// configured on this machine. Any one is enough.
	Detect []string

	// Includes reports whether the agent resolves `@file.md` references inside
	// its instruction file. When true we write sidecar documents and reference
	// them, which keeps the developer's own instruction file short. When false
	// the documents are inlined into the block.
	Includes bool

	// Wrapping is how commands come to be wrapped in this agent: by something
	// sctx installs, or by the agent typing `sctx` itself. It exists because the
	// instruction document has to tell the truth about which, and the two
	// instructions are opposites.
	Wrapping WrapMode

	// MCP is the shape of this agent's MCP registry, and MCPConfig is the file
	// holding it, relative to home. Zero value means sctx does not register
	// servers for this agent — either it has no documented file registry, or we
	// have not verified one — and `sctx setup` says so rather than implying the
	// SynapCTX tools are callable.
	MCP       MCPStyle
	MCPConfig string

	// The JSON dialect, for MCPRemoteJSON agents. Five clients express the same
	// three facts — where the server is, what to send with the request, whether
	// it is on — with four different spellings, and each of these fields is one
	// of them. They are data rather than four near-identical writers because the
	// difference between clients is genuinely only these strings.
	//
	//	kilocode/opencode  "mcp"         url        type "remote"  enabled
	//	gemini             "mcpServers"  httpUrl    -              -
	//	windsurf           "mcpServers"  serverUrl  -              -
	//	crush              "mcp"         url        type "http"    -
	MCPKey      string // object holding the servers
	MCPURLField string // member naming the endpoint
	MCPType     string // transport discriminator; empty when the client has none
	MCPEnabled  bool   // whether the client understands an `enabled` member

	// MinVersion is the earliest release this row's WrapHook capability was
	// verified against, for humans reading `sctx setup` output — never
	// consulted by code, because sctx cannot see which version is installed.
	// Empty for a row with no verified minimum (manual agents, and any hook
	// verified against "whatever ships today" with no lower bound known).
	MinVersion string

	// PluginPath is a JS plugin file, relative to home, that this agent loads
	// from its config directory with no registration. It is how the Kilo/OpenCode
	// family gets the command rewriting that Claude and Gemini get from a hook.
	PluginPath string
}

// WrapMode is how a covered command comes to be run through sctx.
type WrapMode int

const (
	// WrapManual: nothing intercepts the agent's commands, so the agent must
	// write `sctx <cmd>` itself. This is the DEFAULT because it is what happens
	// when we install nothing, and an agent told otherwise silently stops using
	// sctx entirely.
	WrapManual WrapMode = iota
	// WrapHook: `sctx setup --install` wires a pre-tool hook that rewrites
	// covered commands, so the agent writes them naturally.
	WrapHook
	// WrapPlugin: the agent has no hook system but loads plugins, and sctx
	// installs one that rewrites the bash tool's arguments in process. Same
	// result as WrapHook from the agent's point of view.
	WrapPlugin
)

// Wrapped reports whether sctx installs something that wraps this agent's
// commands, so the agent does not have to type `sctx` itself.
func (a Agent) Wrapped() bool { return a.Wrapping == WrapHook || a.Wrapping == WrapPlugin }

// MCPStyle is the format of an agent's MCP server registry.
type MCPStyle int

const (
	// MCPUnmanaged: sctx does not write this agent's MCP registration.
	MCPUnmanaged MCPStyle = iota
	// MCPCodexTOML: `mcp_servers.<name>` tables in ~/.codex/config.toml.
	MCPCodexTOML
	// MCPRemoteJSON: an `mcp` object of `{"type":"remote","url":…,"headers":…}`
	// entries in a JSON config file. Verified against the shipped Kilo 7.4.22
	// binary's own configuration reference; OpenCode is the same engine and the
	// same schema, which is why one style covers both.
	MCPRemoteJSON
)

// KnownAgents is ordered by how likely we are to be right about the convention,
// which is also roughly market share. Order matters only for display.
var KnownAgents = []Agent{
	{
		ID:       "claude",
		Name:     "Claude Code",
		Root:     ".claude/CLAUDE.md",
		Detect:   []string{".claude", ".claude.json"},
		Includes: true,
		Wrapping: WrapHook,
		// Claude Code's MCP registry is managed by `claude mcp` and its own
		// settings, which merge project, user and enterprise scopes. We do not
		// write it: unlike a Codex TOML table, a wrong edit here can disable
		// servers the customer configured elsewhere.
	},
	{
		// Codex reads AGENTS.md — the cross-vendor convention several tools have
		// converged on. No documented include mechanism, so the content is inlined.
		// Codex grew PreToolUse hooks with the same `updatedInput` contract as
		// Claude Code, so it is wrapped too — with one caveat nothing else in
		// this table has: Codex requires a human to TRUST a hook definition
		// (`/hooks`) before it will run it, and until they do the hook is silently
		// skipped. `sctx setup` says so rather than reporting a hook that is
		// installed but inert.
		ID:        "codex",
		Name:      "OpenAI Codex CLI",
		Root:      ".codex/AGENTS.md",
		Detect:    []string{".codex"},
		Wrapping:  WrapHook,
		MCP:       MCPCodexTOML,
		MCPConfig: ".codex/config.toml",
	},
	{
		// Gemini CLI has both halves, verified against its documentation on
		// 2026-08-18: `mcpServers` entries keyed by `httpUrl` with `headers`, and
		// a `BeforeTool` hook whose `hookSpecificOutput.tool_input` MERGES over
		// the model's arguments — which is exactly the rewrite Claude's
		// PreToolUse hook performs, under a different name.
		//
		// Includes stays off: @-imports are reported to work here, but a wrong
		// guess writes a line that silently loads nothing, and inlining is
		// correct either way.
		ID:          "gemini",
		Name:        "Gemini CLI",
		Root:        ".gemini/GEMINI.md",
		Detect:      []string{".gemini"},
		Wrapping:    WrapHook,
		MCP:         MCPRemoteJSON,
		MCPConfig:   ".gemini/settings.json",
		MCPKey:      "mcpServers",
		MCPURLField: "httpUrl",
	},
	{
		// OpenCode and Kilo Code are the same engine: Kilo's own binary still
		// logs `opencode`, reads `opencode.json` as a legacy config name, and
		// ships the identical `mcp` schema. Verified 2026-08-18.
		ID:          "opencode",
		Name:        "OpenCode",
		Root:        ".config/opencode/AGENTS.md",
		Detect:      []string{".config/opencode"},
		Wrapping:    WrapPlugin,
		PluginPath:  ".config/opencode/plugin/sctx.js",
		MCP:         MCPRemoteJSON,
		MCPConfig:   ".config/opencode/opencode.json",
		MCPKey:      "mcp",
		MCPURLField: "url",
		MCPType:     "remote",
		MCPEnabled:  true,
	},
	{
		// Kilo Code. The path here was `.kilocode/rules/synapctx.md` until
		// 2026-08-18, which the shipped 7.4.22 binary treats as LEGACY: it warns
		// "consider migrating to .kilo/rules/", and the file it actually loads
		// for global instructions is AGENTS.md in the config directory
		// (`KILO_CONFIG_DIR ?? ~/.config/kilo`). The old row also detected only
		// `~/.kilocode`, a directory a current install never creates — so Kilo
		// was installed, actively used, and invisible to `sctx setup`.
		//
		// All three roots are detected because they are all still read; the one
		// we WRITE is the modern one, which every version in support loads.
		// The plugin directory is loaded with no config entry and no package
		// install — verified by running 7.4.22 against a sandbox home on
		// 2026-08-18, which is what makes auto-wrap installable here at all.
		ID:          "kilocode",
		Name:        "Kilo Code",
		Root:        ".config/kilo/AGENTS.md",
		Detect:      []string{".config/kilo", ".kilo", ".kilocode"},
		Wrapping:    WrapPlugin,
		PluginPath:  ".config/kilo/plugin/sctx.js",
		MCP:         MCPRemoteJSON,
		MCPConfig:   ".config/kilo/kilo.json",
		MCPKey:      "mcp",
		MCPURLField: "url",
		MCPType:     "remote",
		MCPEnabled:  true,
	},
	{
		// Windsurf's MCP file and its `serverUrl` spelling are from its own
		// documentation (2026-08-18). It is an IDE extension with no documented
		// pre-tool interception point, so commands stay manual here and the
		// instructions say so.
		ID:          "windsurf",
		Name:        "Windsurf",
		Root:        ".codeium/windsurf/memories/global_rules.md",
		Detect:      []string{".codeium/windsurf"},
		MCP:         MCPRemoteJSON,
		MCPConfig:   ".codeium/windsurf/mcp_config.json",
		MCPKey:      "mcpServers",
		MCPURLField: "serverUrl",
	},
	{
		// Crush's `mcp` block with `type: "http"` is from its README and its own
		// config skill (2026-08-18). NOTE for anyone extending this row: Crush
		// SHELL-EXPANDS header values, so a credential containing `$` would be
		// mangled — sctx keys are `sctx_live_` + alphanumerics, which is why
		// writing the token literally is safe here and would not be in general.
		// No documented pre-tool hook, so wrapping stays manual.
		ID:          "crush",
		Name:        "Crush",
		Root:        ".config/crush/AGENTS.md",
		Detect:      []string{".config/crush"},
		MCP:         MCPRemoteJSON,
		MCPConfig:   ".config/crush/crush.json",
		MCPKey:      "mcp",
		MCPURLField: "url",
		MCPType:     "http",
	},
	{
		// Cursor's `preToolUse` hook (matcher `"Shell"`) was introduced in
		// Cursor 1.7 (Oct 2025) and is a different, newer contract than the
		// older `beforeShellExecution` hook: that one is allow/deny only and
		// cannot rewrite a command, which is why this row did not exist before
		// 1.7 shipped. Verified against the rtk reference implementation
		// (github.com/rtk-ai/rtk) rather than Cursor's own docs page, which
		// this change could not fetch (no WebFetch tool available) — cursor.com
		// pages are confirmed to render nothing usable from a plain HTTP GET.
		//
		// Root is `.cursor/AGENTS.md`, by the same reasoning as the Codex row:
		// AGENTS.md is the cross-vendor convention Cursor's own docs list
		// alongside Project/Team/User Rules (fetched 2026-09-04), but WHETHER
		// Cursor reads one from the home directory the way Codex reads
		// `~/.codex/AGENTS.md` is NOT independently confirmed here — Cursor's
		// documented `.cursor/rules` mechanism is explicitly project-scoped
		// ("version-controlled and scoped to your codebase"), and "User
		// Rules" are stored in Cursor's own app settings, not a file this
		// process could write. Includes stays off for the same reason every
		// unverified-include row does: a wrong guess silently loads nothing.
		ID:         "cursor",
		Name:       "Cursor",
		Root:       ".cursor/AGENTS.md",
		Detect:     []string{".cursor"},
		Wrapping:   WrapHook,
		MinVersion: "1.7",
	},
	{
		// GitHub Copilot CLI's `PreToolUse` hook. GitHub's own docs
		// (docs.github.com/en/copilot/reference/hooks-reference, fetched
		// 2026-09-04) accept the event key in either case (`preToolUse` /
		// `PreToolUse`) and describe a camelCase `toolName`/`toolArgs`
		// payload answered with `modifiedArgs`. The rtk reference
		// implementation disagrees on which shape the CLI actually honours in
		// practice: its source records a LIVE verification against Copilot
		// CLI 1.0.73+ (Linux and Windows 11) that the CLI's shell tool is
		// reported under the Claude-shaped `tool_name`/`tool_input.command`
		// schema and answered with `updatedInput` — and rtk's own installer
		// now registers ONLY that schema, having found the documented
		// camelCase registration fires a second, redundant hook invocation
		// per command once both are present. `sctx hook copilot` follows
		// rtk's live-verified path and additionally decodes the documented
		// camelCase shape (answered with `modifiedArgs`, per GitHub's docs)
		// for any install that still has it registered — that second path is
		// UNVERIFIED against a live CLI by this change.
		//
		// Root is `~/.copilot/AGENTS.md`: GitHub documents `AGENTS.md` and
		// `.github/copilot-instructions.md` as project-scoped conventions;
		// a home-directory equivalent is NOT independently verified here, so
		// Includes stays off.
		ID:         "copilot",
		Name:       "GitHub Copilot CLI",
		Root:       ".copilot/AGENTS.md",
		Detect:     []string{".copilot"},
		Wrapping:   WrapHook,
		MinVersion: "1.0.73",
	},
	{
		// Factory Droid's `PreToolUse` hook (matcher `"Execute"`). Verified
		// against docs.factory.ai/cli/configuration/hooks-guide and the rtk
		// reference implementation, which records live verification against
		// Droid v0.140 through v0.164. Droid additionally publishes command
		// deny lists (`commandDenylist`/`commandBlocklist`) across up to four
		// settings scopes; `sctx hook droid` reads them and steps aside on a
		// match rather than rewriting past Droid's own pattern matching — see
		// the hook's own doc comment for the (deliberately over-inclusive)
		// matching rule.
		//
		// Root is `~/.factory/AGENTS.md` by the same extrapolation as the
		// Cursor and Copilot rows above: NOT independently verified for the
		// home directory, so Includes stays off.
		ID:         "droid",
		Name:       "Factory Droid",
		Root:       ".factory/AGENTS.md",
		Detect:     []string{".factory"},
		Wrapping:   WrapHook,
		MinVersion: "0.164.0",
	},
}

// AgentByID returns the agent with this id.
func AgentByID(id string) (Agent, bool) {
	for _, a := range KnownAgents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// DetectPaths lists every path consulted during detection, so `sctx setup` can
// say what it looked for when it found nothing. "No agent detected" is only
// actionable if it comes with the list.
func DetectPaths() []string {
	var out []string
	for _, a := range KnownAgents {
		out = append(out, a.Detect...)
	}
	return out
}
