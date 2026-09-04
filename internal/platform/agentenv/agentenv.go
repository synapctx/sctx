// Package agentenv identifies which coding agent, and which of its sessions,
// is driving the current process. It feeds the telemetry disclosure fields
// "which coding agent ran it" and "the agent's session id" — never anything
// that could identify a person or a machine beyond that.
package agentenv

import (
	"strings"
	"time"
)

// Identity is what agentenv.Detect resolves: the coding agent believed to be
// driving this process, and an opaque session id scoped to one of its runs.
type Identity struct {
	Client    string
	SessionID string
}

// allowedClients is the closed set of values ever reported for Client.
// Anything else — an override typo, a marker nobody has taught this package
// yet — reports as "unknown" rather than leaking an arbitrary string into
// telemetry.
var allowedClients = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"gemini-cli":  true,
	"kilo":        true,
	"opencode":    true,
	"cursor":      true,
	"copilot-cli": true,
	"droid":       true,
	"shell":       true,
	"unknown":     true,
}

// Detect resolves the calling agent from its environment. env is a lookup
// function (normally os.Getenv) rather than the environment itself, so
// callers and tests can supply exactly the variables they mean without a
// real process environment leaking in.
//
// Precedence: an explicit SCT__CLIENT/SCT__SESSION override always wins —
// it is how a caller that already knows its own identity (the hook's session
// hand-off, a CI wrapper) states it directly, rather than through a marker
// this package has to reverse-engineer. Failing that, known per-agent
// environment markers are tried in turn. Nothing matching resolves to
// "shell": a human typing at a terminal, with no session of its own.
func Detect(env func(string) string) Identity {
	if client := env("SCT__CLIENT"); client != "" {
		return Identity{Client: normalizeClient(client), SessionID: sanitizeSession(env("SCT__SESSION"))}
	}

	// Claude Code: verified from its own process environment under both the
	// VS Code extension and the SDK entrypoint, which export CLAUDECODE=1
	// alongside CLAUDE_CODE_SESSION_ID. The plain `claude` CLI has not been
	// independently confirmed to set these — if it does not, it falls through
	// to "shell" below, which is the safe direction: no session is invented.
	if env("CLAUDECODE") == "1" {
		return Identity{Client: "claude-code", SessionID: sanitizeSession(env("CLAUDE_CODE_SESSION_ID"))}
	}

	return Identity{Client: "shell", SessionID: ""}
}

// DetectWithFallback is Detect, plus the session hand-off fallback for an
// agent whose hook runs in a separate process from the one that execs the
// wrapped command (see ReadRecentSessionFile): tried only when env detection
// alone produced no session id, so an agent that already told us who it is
// through its own environment is never second-guessed by a stale file.
func DetectWithFallback(env func(string) string, spoolDir string) Identity {
	id := Detect(env)
	if id.SessionID != "" {
		return id
	}
	if fallback, ok := ReadRecentSessionFile(spoolDir, time.Now()); ok {
		return fallback
	}
	return id
}

// normalizeClient maps a caller-supplied client string onto the allowlist,
// case-insensitively, or reports "unknown" — never an arbitrary value.
func normalizeClient(client string) string {
	lowered := strings.ToLower(strings.TrimSpace(client))
	if allowedClients[lowered] {
		return lowered
	}
	return "unknown"
}

// maxSessionLen bounds SessionID the same way the hook's own session-file
// names are bounded (see agentsetup's first-search counter): long enough for
// any real session id, short enough that nothing pathological reaches a
// filename or a telemetry payload.
const maxSessionLen = 128

// sanitizeSession restricts a caller-supplied session id to
// [A-Za-z0-9._-], truncated to maxSessionLen. Anything else in the input is
// dropped rather than rejected outright: a session id is an opaque
// correlation token, not a value anyone parses, so best-effort sanitization
// costs nothing and an unsanitizable id still degrades to "no session"
// gracefully instead of aborting detection.
func sanitizeSession(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if b.Len() >= maxSessionLen {
			break
		}
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}
