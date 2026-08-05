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
// Absent from this table on purpose, and tracked in business-docs rather than
// guessed at here: project-scoped rule directories (Cursor's `.cursor/rules`,
// Cline's `.clinerules`, Roo's `.roo/rules`), which are per-repository rather
// than per-machine and so belong to a different command; and IDE assistants
// whose "global rules" live in application settings rather than on disk, where
// there is nothing for us to write.
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
}

// KnownAgents is ordered by how likely we are to be right about the convention,
// which is also roughly market share. Order matters only for display.
var KnownAgents = []Agent{
	{
		ID:       "claude",
		Name:     "Claude Code",
		Root:     ".claude/CLAUDE.md",
		Detect:   []string{".claude", ".claude.json"},
		Includes: true,
	},
	{
		// Codex reads AGENTS.md — the cross-vendor convention several tools have
		// converged on. No documented include mechanism, so the content is inlined.
		ID:     "codex",
		Name:   "OpenAI Codex CLI",
		Root:   ".codex/AGENTS.md",
		Detect: []string{".codex"},
	},
	{
		ID:     "gemini",
		Name:   "Gemini CLI",
		Root:   ".gemini/GEMINI.md",
		Detect: []string{".gemini"},
		// Gemini CLI is believed to support @-imports, but "believed" is not a
		// basis for writing a line that loads nothing when wrong. Inline is
		// correct either way; revisit with a verified version.
	},
	{
		ID:     "opencode",
		Name:   "OpenCode",
		Root:   ".config/opencode/AGENTS.md",
		Detect: []string{".config/opencode"},
	},
	{
		ID:     "kilocode",
		Name:   "Kilo Code",
		Root:   ".kilocode/rules/synapctx.md",
		Detect: []string{".kilocode"},
	},
	{
		ID:     "windsurf",
		Name:   "Windsurf",
		Root:   ".codeium/windsurf/memories/global_rules.md",
		Detect: []string{".codeium/windsurf"},
	},
	{
		ID:     "crush",
		Name:   "Crush",
		Root:   ".config/crush/AGENTS.md",
		Detect: []string{".config/crush"},
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
