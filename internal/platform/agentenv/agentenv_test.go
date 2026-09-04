package agentenv

import (
	"testing"
	"time"
)

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want Identity
	}{
		{
			name: "no markers is shell",
			vars: map[string]string{},
			want: Identity{Client: "shell", SessionID: ""},
		},
		{
			name: "claude code marker",
			vars: map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "sess-123"},
			want: Identity{Client: "claude-code", SessionID: "sess-123"},
		},
		{
			name: "claude code marker with no session id",
			vars: map[string]string{"CLAUDECODE": "1"},
			want: Identity{Client: "claude-code", SessionID: ""},
		},
		{
			name: "claude code marker with wrong value does not match",
			vars: map[string]string{"CLAUDECODE": "true"},
			want: Identity{Client: "shell", SessionID: ""},
		},
		{
			name: "explicit override wins over a marker",
			vars: map[string]string{"SCT__CLIENT": "codex", "SCT__SESSION": "abc", "CLAUDECODE": "1"},
			want: Identity{Client: "codex", SessionID: "abc"},
		},
		{
			name: "explicit override is case-insensitive",
			vars: map[string]string{"SCT__CLIENT": "Kilo"},
			want: Identity{Client: "kilo", SessionID: ""},
		},
		{
			name: "unallowlisted override reports unknown",
			vars: map[string]string{"SCT__CLIENT": "made-up-agent"},
			want: Identity{Client: "unknown", SessionID: ""},
		},
		{
			name: "session id is sanitized to the safe alphabet",
			vars: map[string]string{"SCT__CLIENT": "shell", "SCT__SESSION": "a/b c;rm -rf$(x)"},
			want: Identity{Client: "shell", SessionID: "abcrm-rfx"},
		},
		{
			name: "session id is truncated to 128 bytes",
			vars: map[string]string{"SCT__CLIENT": "shell", "SCT__SESSION": string(make([]byte, 200))},
			want: Identity{Client: "shell", SessionID: ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(envFrom(tc.vars))
			if got != tc.want {
				t.Fatalf("Detect(%v) = %+v, want %+v", tc.vars, got, tc.want)
			}
		})
	}
}

func TestSanitizeSessionTruncatesLongIDs(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	got := sanitizeSession(long)
	if len(got) != maxSessionLen {
		t.Fatalf("len = %d, want %d", len(got), maxSessionLen)
	}
}

func TestSessionFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSessionFile(dir, "claude-code", "sess-abc"); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadRecentSessionFile(dir, time.Now())
	if !ok {
		t.Fatal("expected a fresh session file to be readable")
	}
	if got.Client != "claude-code" || got.SessionID != "sess-abc" {
		t.Fatalf("got %+v", got)
	}
}

func TestSessionFileStaleIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSessionFile(dir, "claude-code", "sess-abc"); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(10 * time.Second)
	if _, ok := ReadRecentSessionFile(dir, future); ok {
		t.Fatal("expected a stale session file to be rejected")
	}
}

func TestSessionFileMissingIsFalse(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadRecentSessionFile(dir, time.Now()); ok {
		t.Fatal("expected no session file to report not-ok")
	}
}

func TestDetectWithFallbackPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSessionFile(dir, "cursor", "file-session"); err != nil {
		t.Fatal(err)
	}
	got := DetectWithFallback(envFrom(map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "env-session"}), dir)
	if got.Client != "claude-code" || got.SessionID != "env-session" {
		t.Fatalf("got %+v, want env to win", got)
	}
}

func TestDetectWithFallbackUsesFreshSessionFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSessionFile(dir, "cursor", "file-session"); err != nil {
		t.Fatal(err)
	}
	got := DetectWithFallback(envFrom(map[string]string{}), dir)
	if got.Client != "cursor" || got.SessionID != "file-session" {
		t.Fatalf("got %+v, want the session file to be used", got)
	}
}
