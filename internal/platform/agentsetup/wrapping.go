package agentsetup

// One view over the three ways a command comes to be wrapped, so `sctx setup`
// can answer the only question that matters — "will this agent's commands
// actually go through sctx?" — without the caller knowing which mechanism each
// client happens to offer.
//
// The mechanisms are not interchangeable and cannot be made so: Claude Code and
// Gemini CLI intercept via a hook process, Kilo Code and OpenCode via an
// in-process plugin, and Codex, Windsurf and Crush offer nothing to intercept
// with, so their agents must type `sctx` themselves. What IS uniform is the
// reporting: an agent is wrapped, or it is manual and its instructions say so.

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

// WrapState is the auto-wrap state of one detected agent.
type WrapState struct {
	AgentID   string
	AgentName string
	Mode      agentdoc.WrapMode
	// Where the wiring lives — a settings file or a plugin path. Empty for a
	// manual agent, which has none by definition.
	Path string
	// OK reports whether the wiring sctx owns is in place and current. Always
	// false for a manual agent: nothing is wrong, but nothing is wrapping
	// either, and calling that "ok" is how the manual case stops being visible.
	OK bool
	// NeedsTrust marks the one state sctx installs correctly and cannot finish:
	// Codex will not run a hook until a human reviews and trusts it (`/hooks`),
	// and that trust record is Codex's own — nothing on disk lets us confirm it.
	//
	// It is reported as its own state rather than folded into OK. An [ok] beside
	// something that still needs a human is the same lie as a registered MCP
	// server pointing at a host nothing is listening on: true about what we
	// wrote, false about what the customer has.
	NeedsTrust bool
	// Stale marks the specific failure mode "this WOULD run, but calls a
	// binary sctx setup should replace" (see StaleHookReason) — always false
	// while OK is false for any other reason (not installed, foreign file),
	// so a caller can tell "never wired" apart from "wired to the wrong sctx".
	Stale  bool
	Detail string
}

// InspectWrapping reports auto-wrap for every detected agent.
func InspectWrapping(home, binary string, docs ...Doc) ([]WrapState, error) {
	st, err := Inspect(home, nil, docs...)
	if err != nil {
		return nil, err
	}
	out := make([]WrapState, 0, len(st.Targets))
	for _, t := range st.Targets {
		out = append(out, wrapStateFor(home, t.Agent, binary))
	}
	return out, nil
}

func wrapStateFor(home string, a Agent, binary string) WrapState {
	ws := WrapState{AgentID: a.ID, AgentName: a.Name, Mode: a.Wrapping}
	switch a.Wrapping {
	case agentdoc.WrapHook:
		// Codex keeps its hooks in TOML beside its MCP registrations, and adds a
		// trust step no other client has. Same mechanism, different file format
		// and one more thing to say.
		if a.ID == "codex" {
			cs, err := InspectCodexHooks(home, binary)
			ws.Path = cs.ConfigPath
			switch {
			case err != nil:
				ws.Detail = err.Error()
			case len(cs.Conflicts) > 0:
				ws.Detail = strings.Join(cs.Conflicts, ", ")
			case !cs.Installed:
				ws.Detail = "hook not wired"
			case cs.Missing != "":
				ws.Detail = "the sctx it calls is gone: " + cs.Missing
			case cs.Stale:
				ws.Stale = true
				ws.Detail = "hook is out of date: " + cs.StaleReason
			default:
				ws.OK = true
				ws.NeedsTrust = true
				ws.Detail = "installed — Codex runs it only after you trust it once: /hooks"
			}
			return ws
		}
		// Cursor, Copilot CLI and Factory Droid each keep the hook in a
		// config file of their own shape, not the shared settings.json map
		// hooks.go manages for Claude/Gemini -- same reasoning as Codex's
		// TOML file above, three more times.
		if simple, ok := simpleHookStatusFor(a.ID, home, binary); ok {
			ws.Path = simple.path
			switch {
			case simple.err != nil:
				ws.Detail = simple.err.Error()
			case simple.foreign:
				ws.Detail = "a file of this name exists that sctx did not write"
			case !simple.installed:
				ws.Detail = "hook not wired"
			case simple.missing != "":
				ws.Detail = "the sctx it calls is gone: " + simple.missing
			case simple.stale:
				ws.Stale = true
				ws.Detail = "hook is out of date: " + simple.staleReason
			default:
				ws.OK = true
				ws.Detail = "rewrites covered commands before they run"
			}
			return ws
		}
		path, states, err := InspectAgentHooks(home, a, binary)
		ws.Path = path
		if err != nil {
			ws.Detail = err.Error()
			return ws
		}
		missing, stale := 0, 0
		var staleReason string
		for _, hs := range states {
			switch {
			case !hs.Installed:
				missing++
			case hs.Stale:
				stale++
				staleReason = hs.StaleReason
			}
		}
		ws.OK = missing == 0 && stale == 0 && len(states) > 0
		ws.Detail = "rewrites covered commands before they run"
		switch {
		case missing > 0:
			ws.Detail = fmt.Sprintf("%d hook(s) not wired", missing)
		case stale > 0:
			ws.Stale = true
			if stale == 1 {
				ws.Detail = "hook is out of date: " + staleReason
			} else {
				ws.Detail = fmt.Sprintf("%d hook(s) out of date, e.g. %s", stale, staleReason)
			}
		}
	case agentdoc.WrapPlugin:
		ps, err := InspectPlugin(home, a, binary)
		ws.Path = ps.Path
		if err != nil {
			ws.Detail = err.Error()
			return ws
		}
		switch {
		case ps.Foreign:
			ws.Detail = "a file of this name exists that sctx did not write"
		case !ps.Installed:
			ws.Detail = "plugin not installed"
		case ps.Missing != "":
			ws.Detail = "the sctx it calls is gone: " + ps.Missing
		case ps.Stale:
			ws.Stale = true
			ws.Detail = "plugin is out of date: " + ps.StaleReason
		default:
			ws.OK = true
			ws.Detail = "rewrites covered commands before they run"
		}
	default:
		ws.Detail = "no interception point in this client — its instructions tell it to type `sctx` itself"
	}
	return ws
}

// InstallWrapping wires auto-wrap for every detected agent that supports it.
//
// Errors come back alongside the successes rather than aborting: one agent with
// a settings file we cannot parse must not cost every other agent its wrapping.
func InstallWrapping(home, binary string, docs ...Doc) ([]string, []error) {
	st, err := Inspect(home, nil, docs...)
	if err != nil {
		return nil, []error{err}
	}
	var changed []string
	var problems []error
	for _, t := range st.Targets {
		var (
			c []string
			e error
		)
		switch t.Wrapping {
		case agentdoc.WrapHook:
			switch t.ID {
			case "codex":
				c, e = InstallCodexHooks(home, binary)
			case "cursor":
				c, e = InstallCursorHooks(home, binary)
			case "copilot":
				c, e = InstallCopilotHooks(home, binary)
			case "droid":
				c, e = InstallDroidHooks(home, binary)
			default:
				c, e = InstallAgentHooks(home, t.Agent, binary)
			}
		case agentdoc.WrapPlugin:
			c, e = InstallPlugin(home, t.Agent, binary)
		default:
			continue
		}
		if e != nil {
			problems = append(problems, fmt.Errorf("%s: %w", t.Name, e))
			continue
		}
		changed = append(changed, c...)
	}
	return changed, problems
}

// simpleHookState is the common shape of Cursor/Copilot/Droid inspection
// results, so wrapStateFor can report all three the same way it reports
// Codex, without three near-identical switches inline.
type simpleHookState struct {
	path        string
	installed   bool
	stale       bool
	staleReason string
	foreign     bool
	missing     string
	err         error
}

// simpleHookStatusFor inspects the hook file for one of the three agents
// whose config is a dedicated JSON file rather than Claude/Gemini's shared
// settings.json. ok is false for any other agent ID, so the caller falls
// through to the settings.json path unchanged.
func simpleHookStatusFor(id, home, binary string) (simpleHookState, bool) {
	switch id {
	case "cursor":
		cs, err := InspectCursorHooks(home, binary)
		return simpleHookState{path: cs.ConfigPath, installed: cs.Installed, stale: cs.Stale, staleReason: cs.StaleReason, missing: cs.Missing, err: err}, true
	case "copilot":
		cs, err := InspectCopilotHooks(home, binary)
		return simpleHookState{path: cs.ConfigPath, installed: cs.Installed, stale: cs.Stale, staleReason: cs.StaleReason, foreign: cs.Foreign, missing: cs.Missing, err: err}, true
	case "droid":
		cs, err := InspectDroidHooks(home, binary)
		return simpleHookState{path: cs.ConfigPath, installed: cs.Installed, stale: cs.Stale, staleReason: cs.StaleReason, missing: cs.Missing, err: err}, true
	}
	return simpleHookState{}, false
}
