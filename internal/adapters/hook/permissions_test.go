package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashRuleMayMatch(t *testing.T) {
	cases := []struct {
		name, rule, cmd string
		want            bool
	}{
		{"legacy prefix", "git push:*", "git push origin main", true},
		{"legacy prefix bare", "git push:*", "git push", true},
		{"legacy prefix not other sub", "git push:*", "git pull origin main", false},
		{"legacy prefix no word cut", "git push:*", "legit pusher", false},
		{"exact", "git status", "git status", true},
		{"exact in chain", "git push origin main", "cd x && git push origin main", true},
		{"wildcard word", "git push *", "git push -f", true},
		{"wildcard no space matches glued", "git push*", "git pushx", true},
		{"leading wildcard", "* --force", "git push --force", true},
		{"leading wildcard miss", "* --force", "git push", false},
		{"path-qualified", "git push:*", "/usr/bin/git push", true},
		{"inside bash -c", "git push:*", `bash -c "git push origin main"`, true},
		{"env prefix", "git push:*", "FOO=1 git push", true},
		{"unrelated", "git push:*", "ls -la", false},
		{"middle wildcard", "docker * rm", "docker container rm", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bashRuleMayMatch(tc.rule, tc.cmd); got != tc.want {
				t.Fatalf("bashRuleMayMatch(%q, %q) = %v, want %v", tc.rule, tc.cmd, got, tc.want)
			}
		})
	}
}

func TestBashRuleContent(t *testing.T) {
	cases := []struct {
		entry   string
		content string
		all     bool
		isBash  bool
	}{
		{"Bash(git push:*)", "git push:*", false, true},
		{"Bash", "", true, true},
		{"Bash(*)", "", true, true},
		{"Bash()", "", true, true},
		{"Read(./.env)", "", false, false},
		{"WebFetch(domain:x.com)", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.entry, func(t *testing.T) {
			c, all, isBash := bashRuleContent(tc.entry)
			if c != tc.content || all != tc.all || isBash != tc.isBash {
				t.Fatalf("got (%q,%v,%v), want (%q,%v,%v)", c, all, isBash, tc.content, tc.all, tc.isBash)
			}
		})
	}
}

// fakeClaudeEnv points HOME and CLAUDE_CONFIG_DIR at temp dirs and returns
// (userSettingsPath, projectDir).
func fakeClaudeEnv(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(cfgDir, "settings.json"), t.TempDir()
}

func writeFileT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionStepAside(t *testing.T) {
	cases := []struct {
		name  string
		user  string // user settings.json ("" = absent)
		proj  string // project .claude/settings.json
		local string // project .claude/settings.local.json
		cmd   string
		mode  string
		// autoOptIn sets SCT__AUTO_MODE_STEP_ASIDE=true.
		autoOptIn bool
		want      bool
	}{
		{name: "no settings", cmd: "git push origin main", want: false},
		{name: "user deny", user: `{"permissions":{"deny":["Bash(git push:*)"]}}`, cmd: "git push origin main", want: true},
		{name: "user ask", user: `{"permissions":{"ask":["Bash(git push:*)"]}}`, cmd: "git push", want: true},
		{name: "project deny", proj: `{"permissions":{"deny":["Bash(rm -rf:*)"]}}`, cmd: "rm -rf build", want: true},
		{name: "local ask", local: `{"permissions":{"ask":["Bash(git commit:*)"]}}`, cmd: "git commit -m x", want: true},
		{name: "deny other cmd", user: `{"permissions":{"deny":["Bash(git push:*)"]}}`, cmd: "git status", want: false},
		{name: "bare Bash deny", user: `{"permissions":{"deny":["Bash"]}}`, cmd: "ls", want: true},
		{name: "allow rule alone does not step aside", user: `{"permissions":{"allow":["Bash(git push:*)"]}}`, cmd: "git push", want: false},
		{name: "non-bash deny ignored", user: `{"permissions":{"deny":["Read(./.env)"]}}`, cmd: "git push", want: false},
		{name: "unparseable user file", user: `{not json`, cmd: "ls", want: true},
		{name: "wrong type", user: `{"permissions":{"deny":"Bash(ls)"}}`, cmd: "ls", want: true},
		{name: "empty file ok", user: ``, cmd: "ls", want: false},
		{name: "BOM tolerated", user: "\xef\xbb\xbf" + `{"permissions":{"deny":["Bash(git push:*)"]}}`, cmd: "git push", want: true},
		{name: "auto mode rewrites by default", cmd: "ls", mode: "auto", want: false},
		{name: "auto mode opt-in steps aside", cmd: "ls", mode: "auto", autoOptIn: true, want: true},
		{name: "auto mode still honours deny", user: `{"permissions":{"deny":["Bash(git push:*)"]}}`, cmd: "git push", mode: "auto", want: true},
		{name: "default mode", cmd: "ls", mode: "default", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userPath, proj := fakeClaudeEnv(t)
			if tc.autoOptIn {
				t.Setenv("SCT__AUTO_MODE_STEP_ASIDE", "true")
			} else {
				t.Setenv("SCT__AUTO_MODE_STEP_ASIDE", "")
			}
			if tc.user != "" || tc.name == "empty file ok" {
				writeFileT(t, userPath, tc.user)
			}
			if tc.proj != "" {
				writeFileT(t, filepath.Join(proj, ".claude", "settings.json"), tc.proj)
			}
			if tc.local != "" {
				writeFileT(t, filepath.Join(proj, ".claude", "settings.local.json"), tc.local)
			}
			_, got := permissionStepAside(tc.cmd, tc.mode, proj)
			if got != tc.want {
				t.Fatalf("permissionStepAside(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestClaudeSettingsDiscovery(t *testing.T) {
	userPath, proj := fakeClaudeEnv(t)
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := claudeSettingsFiles(os.Getenv("HOME"), sub)
	has := func(p string) bool {
		for _, f := range files {
			if f == p {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		userPath, // CLAUDE_CONFIG_DIR honoured, not ~/.claude
		filepath.Join(proj, ".claude", "settings.json"),
		filepath.Join(proj, ".claude", "settings.local.json"),
		filepath.Join(sub, ".claude", "settings.json"),
	} {
		if !has(want) {
			t.Errorf("settings discovery missing %s", want)
		}
	}
	if has(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")) {
		t.Error("~/.claude consulted although CLAUDE_CONFIG_DIR is set")
	}
}

func TestClaudeSettingsDiscoveryDefaultsToHomeDotClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	files := claudeSettingsFiles(home, "")
	if len(files) == 0 || files[0] != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("first file = %v, want ~/.claude/settings.json", files)
	}
}

func TestClaudeAllowRuleWarnings(t *testing.T) {
	cases := []struct {
		name  string
		allow string
		warn  bool
	}{
		{"sctx colon star", `"Bash(sctx:*)"`, true},
		{"sctx space star", `"Bash(sctx *)"`, true},
		{"sctx glued star", `"Bash(sctx*)"`, true},
		{"narrow sctx", `"Bash(sctx git status:*)"`, false},
		{"exact sctx", `"Bash(sctx gain)"`, false},
		{"other", `"Bash(git status:*)"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userPath, proj := fakeClaudeEnv(t)
			writeFileT(t, userPath, `{"permissions":{"allow":[`+tc.allow+`]}}`)
			got := ClaudeAllowRuleWarnings(os.Getenv("HOME"), proj)
			if (len(got) > 0) != tc.warn {
				t.Fatalf("warnings = %v, want warn=%v", got, tc.warn)
			}
			if tc.warn && !strings.Contains(got[0], userPath) {
				t.Fatalf("warning does not name the file: %v", got)
			}
		})
	}
}

// TestRunClaudeStepsAsideForDenyRule drives the real hook: a command that
// would be rewritten is left alone once a deny rule covers it.
func TestRunClaudeStepsAsideForDenyRule(t *testing.T) {
	run := func(t *testing.T, payload string) string {
		var out bytes.Buffer
		RunClaude(nil, strings.NewReader(payload), &out, "test")
		return out.String()
	}
	t.Run("rewrites without rules", func(t *testing.T) {
		_, proj := fakeClaudeEnv(t)
		got := run(t, `{"tool_name":"Bash","cwd":`+quoteJSON(proj)+`,"tool_input":{"command":"git status"}}`)
		if !strings.Contains(got, "sctx git status") {
			t.Fatalf("expected rewrite, got %q", got)
		}
	})
	t.Run("deny rule blocks rewrite", func(t *testing.T) {
		userPath, proj := fakeClaudeEnv(t)
		writeFileT(t, userPath, `{"permissions":{"deny":["Bash(git status:*)"]}}`)
		got := run(t, `{"tool_name":"Bash","cwd":`+quoteJSON(proj)+`,"tool_input":{"command":"git status"}}`)
		if strings.Contains(got, "updatedInput") {
			t.Fatalf("rewrote a denied command: %q", got)
		}
	})
	t.Run("auto mode rewrites by default", func(t *testing.T) {
		_, proj := fakeClaudeEnv(t)
		t.Setenv("SCT__AUTO_MODE_STEP_ASIDE", "")
		got := run(t, `{"tool_name":"Bash","permission_mode":"auto","cwd":`+quoteJSON(proj)+`,"tool_input":{"command":"git status"}}`)
		if !strings.Contains(got, "sctx git status") {
			t.Fatalf("expected rewrite in auto mode, got %q", got)
		}
	})
	t.Run("auto mode opt-in blocks rewrite", func(t *testing.T) {
		_, proj := fakeClaudeEnv(t)
		t.Setenv("SCT__AUTO_MODE_STEP_ASIDE", "true")
		got := run(t, `{"tool_name":"Bash","permission_mode":"auto","cwd":`+quoteJSON(proj)+`,"tool_input":{"command":"git status"}}`)
		if strings.Contains(got, "updatedInput") {
			t.Fatalf("rewrote in auto mode despite opt-in: %q", got)
		}
	})
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
