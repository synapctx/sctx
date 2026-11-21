package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBashRuleMayMatchWrappedForms: a glued `*` needs only a LEADING word
// boundary, and the glob is tried from every token of every raw segment.
func TestBashRuleMayMatchWrappedForms(t *testing.T) {
	cases := []struct {
		name, rule, cmd string
		want            bool
	}{
		{"env prefix glued star", "make deploy*", "ENV=prod make deploy-prod", true},
		{"timeout glued star", "make deploy*", "timeout 600 make deploy-prod", true},
		{"abs path glued star", "make deploy*", "/usr/bin/make deploy-prod", true},
		{"newline segment", "make deploy*", "cd infra\nmake deploy-prod", true},
		{"semicolon segment", "make deploy*", "cd infra;make deploy-prod", true},
		{"python glued", "python*", "PYTHONPATH=. python3 -m pytest", true},
		{"glob from later token", "make deploy*", "ENV=prod nice make deploy-prod", true},
		{"basenamed token glob", "make * prod", "/usr/bin/make deploy prod", true},
		{"glued star leading boundary kept", "make deploy*", "unmake deploy-prod", false},
		{"glued star unrelated", "make deploy*", "make build", false},
		{"legacy prefix still word-bounded", "git push:*", "git pushx", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bashRuleMayMatch(tc.rule, tc.cmd); got != tc.want {
				t.Fatalf("bashRuleMayMatch(%q, %q) = %v, want %v", tc.rule, tc.cmd, got, tc.want)
			}
		})
	}
}

// TestPermissionStepAsideFileRules: Read/Edit/Write rules plus a file-reading
// program we wrap steps aside (fail-safe).
func TestPermissionStepAsideFileRules(t *testing.T) {
	cases := []struct {
		name, user, cmd string
		want            bool
	}{
		{"read deny + cat", `{"permissions":{"deny":["Read(./.env)"]}}`, "cat .env", true},
		{"edit ask + grep in chain", `{"permissions":{"ask":["Edit(./x)"]}}`, "cd a && grep foo b", true},
		{"write deny + path rg", `{"permissions":{"deny":["Write(**)"]}}`, "/opt/homebrew/bin/rg foo", true},
		{"read deny + go test", `{"permissions":{"deny":["Read(./.env)"]}}`, "go test ./...", false},
		{"allow read rule ignored", `{"permissions":{"allow":["Read(./.env)"]}}`, "cat .env", false},
		{"no rule + cat", ``, "cat .env", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userPath, proj := fakeClaudeEnv(t)
			t.Setenv("SCT__AUTO_MODE_STEP_ASIDE", "")
			if tc.user != "" {
				writeFileT(t, userPath, tc.user)
			}
			if _, got := permissionStepAside(tc.cmd, "", proj); got != tc.want {
				t.Fatalf("permissionStepAside(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestClaudeSettingsManagedPaths(t *testing.T) {
	found := false
	for _, d := range managedSettingsDirs("windows") {
		if d == `C:\Program Files\ClaudeCode` {
			found = true
		}
	}
	if !found {
		t.Error("windows managed dir missing")
	}
	for _, d := range managedSettingsDirs("linux") {
		if strings.HasPrefix(d, "C:") {
			t.Errorf("windows dir %q listed on linux", d)
		}
	}

	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "managed-settings.d", "10-a.json"), `{}`)
	writeFileT(t, filepath.Join(dir, "managed-settings.d", "20-b.json"), `{}`)
	writeFileT(t, filepath.Join(dir, "managed-settings.d", "note.txt"), `x`)
	got := managedSettingsFilesIn(dir)
	want := []string{
		filepath.Join(dir, "managed-settings.json"),
		filepath.Join(dir, "managed-settings.d", "10-a.json"),
		filepath.Join(dir, "managed-settings.d", "20-b.json"),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("managedSettingsFilesIn = %v, want %v", got, want)
	}
}

func TestRewriteDisabledValue(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "Yes", "on", " ON "} {
		if !rewriteDisabledValue(v) {
			t.Errorf("%q should disable", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "2"} {
		if rewriteDisabledValue(v) {
			t.Errorf("%q should not disable", v)
		}
	}
	if _, ok := rewrite("SCT__REWRITE_DISABLED=YES go test ./..."); ok {
		t.Error("inline YES must opt out")
	}
	t.Setenv("SCT__REWRITE_DISABLED", "on")
	if _, ok := rewriteForAgent("go test ./...", "dev", "gemini-cli", ""); ok {
		t.Error("process-wide 'on' must disable rewriteForAgent")
	}
}

func TestStepAsideNote(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	if _, ok := ClaudeStepAsideNote(time.Now()); ok {
		t.Fatal("note before any record")
	}
	recordStepAside("permission rule Bash(x) in /a")
	if _, ok := ClaudeStepAsideNote(time.Now()); ok {
		t.Fatal("a plain rule match is not a file fault and must not be recorded")
	}
	recordStepAside(unreadableSettingsPrefix + "/p/settings.json")
	note, ok := ClaudeStepAsideNote(time.Now())
	if !ok || !strings.Contains(note, "/p/settings.json") {
		t.Fatalf("note = %q, %v", note, ok)
	}
	if _, ok := ClaudeStepAsideNote(time.Now().Add(8 * 24 * time.Hour)); ok {
		t.Fatal("note older than 7 days must be hidden")
	}
	fi, err := os.Stat(filepath.Join(spoolDir(), "permission-stepaside"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", fi, err)
	}
}

func TestPeelHardening(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"timeout -s TERM", "timeout -s TERM 60 go test", "timeout -s TERM 60 sctx go test"},
		{"timeout -s 15", "timeout -s 15 60 go test", "timeout -s 15 60 sctx go test"},
		{"timeout -s SIGTERM", "timeout -s SIGTERM 60 go test", "timeout -s SIGTERM 60 sctx go test"},
		{"timeout -s INT", "timeout -s INT 60 go test", "timeout -s INT 60 sctx go test"},
		{"timeout -s 2", "timeout -s 2 60 go test", "timeout -s 2 60 sctx go test"},
		{"timeout -s SIGINT", "timeout -s SIGINT 60 go test", "timeout -s SIGINT 60 sctx go test"},
		{"timeout --signal=TERM", "timeout --signal=TERM 60 go test", "timeout --signal=TERM 60 sctx go test"},
		{"timeout -s KILL", "timeout -s KILL 60 go test", ""},
		{"timeout -s HUP", "timeout -s HUP 60 go test", ""},
		{"timeout --signal=9", "timeout --signal=9 60 go test", ""},
		{"timeout -k", "timeout -k 5 60 go test", ""},
		{"timeout --kill-after", "timeout --kill-after=5s 60 go test", ""},
		{"env -i", "env -i go test", ""},
		{"env --ignore-environment", "env --ignore-environment go test", ""},
		{"env -u PATH", "env -u PATH go test", ""},
		{"env --unset=PATH", "env --unset=PATH go test", ""},
		{"env PATH=", "env PATH=/bin go test", ""},
		{"env FOO ok", "env FOO=1 go test", "env FOO=1 sctx go test"},
		{"env -u HOME ok", "env -u HOME go test", "env -u HOME sctx go test"},
		{"leading PATH=", "PATH=/bin go test", ""},
		{"leading PATH= after other", "FOO=1 PATH=/bin go test", ""},
		{"leading PATH= before wrapper", "PATH=/bin timeout 5 go test", ""},
		{"PATHX is not PATH", "PATHX=/bin go test", "PATHX=/bin sctx go test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewrite(tc.in)
			if tc.want == "" {
				if ok || got != tc.in {
					t.Fatalf("rewrite(%q) = %q, %v; want declined", tc.in, got, ok)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("rewrite(%q) = %q (%v), want %q", tc.in, got, ok, tc.want)
			}
		})
	}
}

func TestStreamingWatchDeclines(t *testing.T) {
	for _, in := range []string{
		"yarn tsc -w",
		"pnpm vitest -w",
		"pnpm exec vitest -w",
		"pnpm jest --watch",
		"yarn vitest --watch",
		"yarn eslint --watch .",
		"yarn jest --watchAll",
		"vitest watch",
		"vitest dev",
		"vitest --ui",
		"npx vitest watch",
		"npx -y vitest dev",
		"pnpm vitest --ui",
		"yarn vitest dev",
		"bunx vitest watch",
		"uv run --with x tail -f log",
		"uv run -- tail -f log",
		`uv run "tail" -f log`,
		"poetry run --quiet tail -f log",
	} {
		t.Run(in, func(t *testing.T) {
			if got, ok := rewrite(in); ok {
				t.Fatalf("rewrite(%q) = %q; a following command must not be wrapped", in, got)
			}
		})
	}
	for _, in := range []string{"vitest run", "pnpm vitest run", "uv run pytest"} {
		t.Run("keeps "+in, func(t *testing.T) {
			if _, ok := rewrite(in); !ok {
				t.Fatalf("rewrite(%q) declined; coverage lost", in)
			}
		})
	}
}

func TestGapMeterSeesThroughWrappers(t *testing.T) {
	for _, cmd := range []string{
		"timeout 60 sctx go test ./...",
		"env SCT__REWRITE_DISABLED=1 go test ./...",
		"env SCT__REWRITE_DISABLED=yes go test ./...",
		"FOO=1 timeout 5 /usr/local/bin/sctx go test",
	} {
		t.Run(cmd, func(t *testing.T) {
			if seg, ok := gapSegment(cmd); ok {
				t.Fatalf("gapSegment(%q) = %q; not a gap", cmd, seg)
			}
		})
	}
	for _, cmd := range []string{
		"timeout 10 tail -f app.log",
		"env FOO=1 tail -f app.log",
		"nice -n 5 kubectl logs -f pod",
		"uv run --with x tail -f log",
	} {
		t.Run("streaming "+cmd, func(t *testing.T) {
			if seg, ok := gapSegment(cmd); ok {
				t.Fatalf("gapSegment(%q) = %q; streaming is a decline", cmd, seg)
			}
			seg, reason, ok := declineSegment(cmd)
			if !ok || reason != "streaming" {
				t.Fatalf("declineSegment(%q) = %q, %q, %v; want streaming decline", cmd, seg, reason, ok)
			}
			if !strings.Contains(seg, "tail") && !strings.Contains(seg, "kubectl") {
				t.Fatalf("decline segment %q does not name the inner program", seg)
			}
		})
	}
}

// TestUsesFileReadingProgramJudgesTheEffectiveProgram is the re-review finding:
// judging every WORD made a single `Read(./.env)` rule cost the saving on every
// piped command (`go test | tail -5`), while missing `git show HEAD:.env`.
func TestUsesFileReadingProgramJudgesTheEffectiveProgram(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"cat .env", true},
		{"timeout 5 cat .env", true},
		{"FOO=1 /bin/cat .env", true},
		{"cd app && head -3 secrets.txt", true},
		{"git show HEAD:.env", true},
		{"git -C repo show HEAD:.env", true},
		{"jq . secrets.json", true},
		{"go test ./... 2>&1 | tail -5", false},
		{"go build ./... | head", false},
		{"git status", false},
		{"git log --oneline", false},
		{"make build", false},
		{"echo cat", false},
	}
	for _, tc := range tests {
		if got := usesFileReadingProgram(tc.cmd); got != tc.want {
			t.Errorf("usesFileReadingProgram(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

// TestRunClaudeRecordsUnreadableSettingsStepAside drives the real hook: a
// malformed settings file makes it step aside AND leaves the note `sctx setup`
// surfaces (it was computed and then discarded).
func TestRunClaudeRecordsUnreadableSettingsStepAside(t *testing.T) {
	userPath, proj := fakeClaudeEnv(t)
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	writeFileT(t, userPath, `{bad`)
	var out bytes.Buffer
	RunClaude(nil, strings.NewReader(`{"tool_name":"Bash","cwd":`+quoteJSON(proj)+`,"tool_input":{"command":"git status"}}`), &out, "test")
	if strings.Contains(out.String(), "updatedInput") {
		t.Fatalf("rewrote despite malformed settings: %q", out.String())
	}
	note, ok := ClaudeStepAsideNote(time.Now())
	if !ok || !strings.Contains(note, userPath) {
		t.Fatalf("step-aside note = %q, %v; want it to name %s", note, ok, userPath)
	}
}
