package agentenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// sessionFileName is the hand-off file a PreToolUse hook writes on every tool
// call it sees (whether or not it rewrites the command), so the SEPARATE
// process that later runs the wrapped command — which inherits no in-process
// state from the hook — can still learn which agent and session drove it.
//
// It lives under the same spool tree as the first-search counter
// (<spoolDir>/sessions/<session_id>, see agentsetup), a different filename so
// the two never collide.
const sessionFileName = "current"

// SessionRecord is what the hook hands off and the run pipeline reads back.
type SessionRecord struct {
	Client    string `json:"agent"`
	SessionID string `json:"sid"`
	AtUnix    int64  `json:"at"`
}

func sessionFilePath(spoolDir string) string {
	return filepath.Join(spoolDir, "sessions", sessionFileName)
}

// WriteSessionFile records the calling hook's agent and session id for the
// wrapped-command process to pick up. Best-effort: the caller is a fail-open
// hook, so any error here is swallowed by design (see agentenv.Detect's
// callers) rather than surfaced.
//
// Written atomically (temp file + rename) so a reader never observes a
// partial write, and 0600 because a session id, while opaque, is still
// per-developer state with no reason to be world-readable.
func WriteSessionFile(spoolDir, client, sessionID string) error {
	if spoolDir == "" || sessionID == "" {
		return nil
	}
	path := sessionFilePath(spoolDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	rec := SessionRecord{Client: client, SessionID: sessionID, AtUnix: time.Now().Unix()}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), sessionFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// maxSessionFileAge is how stale the hand-off file may be and still be
// trusted. It exists on the wrapped command's own process, started moments
// after the hook ran on the same tool call, so anything older is very likely
// a PREVIOUS session's leftover file rather than this one's — and using it
// would misattribute a plain shell command to whichever agent last ran.
const maxSessionFileAge = 2 * time.Second

// ReadRecentSessionFile returns the hand-off record if one exists and was
// written within maxSessionFileAge, for the fallback case where the wrapped
// process itself carries no env marker (an agent whose hook fires in a
// separate process from the one that execs the command). ok is false for a
// missing, unreadable, unparseable or stale file — never an error, since this
// is a best-effort fallback on the hot path of every wrapped command.
func ReadRecentSessionFile(spoolDir string, now time.Time) (Identity, bool) {
	if spoolDir == "" {
		return Identity{}, false
	}
	data, err := os.ReadFile(sessionFilePath(spoolDir))
	if err != nil {
		return Identity{}, false
	}
	var rec SessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return Identity{}, false
	}
	if rec.SessionID == "" {
		return Identity{}, false
	}
	age := now.Sub(time.Unix(rec.AtUnix, 0))
	if age < 0 || age > maxSessionFileAge {
		return Identity{}, false
	}
	return Identity{Client: normalizeClient(rec.Client), SessionID: sanitizeSession(rec.SessionID)}, true
}
