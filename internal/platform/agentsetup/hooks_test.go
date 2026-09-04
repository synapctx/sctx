package agentsetup

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// theRealSettings is this developer's actual settings.json shape on 2026-08-01:
// the Bash hook already present with a --fallback flag, plus plugins, theme and
// permissions that have nothing to do with us. Every test here defends
// something that file would have lost.
const theRealSettings = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/Users/x/.local/bin/sctx hook claude --fallback legacy-wrapper"}]}
    ]
  },
  "enabledPlugins": {"context7@claude-plugins-official": true},
  "theme": "dark",
  "permissions": {"allow": ["Bash(ssh root@example:*)"]}
}`

func settingsAt(t *testing.T, home, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("we wrote unparseable JSON into the developer's settings: %v", err)
	}
	return doc
}

// THE one that matters. settings.json is the developer's file — plugins, theme,
// permissions, their own hooks. We add entries; we never rewrite it.
func TestInstallPreservesEverythingWeDoNotOwn(t *testing.T) {
	home := t.TempDir()
	path := settingsAt(t, home, theRealSettings)

	if _, err := InstallHooks(home, "/Users/x/.local/bin/sctx"); err != nil {
		t.Fatal(err)
	}
	doc := readSettings(t, path)

	if doc["theme"] != "dark" {
		t.Error("theme was lost")
	}
	if _, ok := doc["enabledPlugins"].(map[string]any)["context7@claude-plugins-official"]; !ok {
		t.Error("enabledPlugins was lost")
	}
	if _, ok := doc["permissions"]; !ok {
		t.Error("permissions were lost")
	}
}

// A developer whose Bash hook carries extra flags — e.g. a `--fallback` from an
// older install — has OUR hook. An equality check would install a second copy
// that then fires on every single command, forever.
//
// The entry is NORMALISED in place (see dropRemovedFlags), never duplicated:
// exactly one Bash hook, still ours, with flags we no longer accept removed.
func TestAnExistingHookWithExtraFlagsIsNotDuplicated(t *testing.T) {
	home := t.TempDir()
	path := settingsAt(t, home, theRealSettings)

	changed, err := InstallHooks(home, "/Users/x/.local/bin/sctx")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changed {
		// Narrowed to the Bash group: sctx installs a SECOND PreToolUse hook
		// (Grep|Glob|Agent) that this fixture does not have, so its legitimate
		// installation must not read as a duplicate of the developer's own.
		if strings.Contains(c, "PreToolUse(Bash)") {
			t.Errorf("reinstalled a hook that was already present: %q", c)
		}
	}
	raw, _ := os.ReadFile(path)
	// Quote-terminated: `sctx hook claude` is a PREFIX of `sctx hook
	// claude-post-tool`, which this file installs alongside it.
	if n := strings.Count(string(raw), "sctx hook claude\""); n != 1 {
		t.Errorf("the developer's Bash hook appears %d times, want exactly 1", n)
	}
	if strings.Contains(string(raw), "--fallback") {
		t.Errorf("a removed flag survived reinstall:\n%s", raw)
	}
	if n := strings.Count(string(raw), `"matcher": "Bash"`); n != 1 {
		t.Errorf("Bash matcher group duplicated (%d groups)", n)
	}
}

func TestHookInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	path := settingsAt(t, home, theRealSettings)
	if _, err := InstallHooks(home, "sctx"); err != nil {
		t.Fatal(err)
	}
	changed, err := InstallHooks(home, "sctx")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Errorf("second install changed %v", changed)
	}
	raw, _ := os.ReadFile(path)
	// Every verb, not just the post-tool one. Two hooks now share the PreToolUse
	// event, and a matcher-group bug would duplicate the SECOND group while
	// leaving the first — invisible to a check that only counts one subcommand.
	for _, spec := range ClaudeHooks("sctx") {
		if n := strings.Count(string(raw), "hook "+spec.Subcommand+"\""); n != 1 {
			t.Errorf("%s(%s) appears %d times after a second install, want exactly 1:\n%s",
				spec.Event, spec.Matcher, n, raw)
		}
	}
}

// Two groups with the same matcher both fire. That is legal, invisible in the
// file, and doubles the work on every matching tool call.
func TestASecondMatcherGroupIsNeverCreated(t *testing.T) {
	home := t.TempDir()
	// The matcher is read from the spec rather than hardcoded: it changed on
	// 2026-08-02 when Bash joined it, and a literal here silently stopped
	// testing anything — the fixture no longer matched, so a second group was
	// legitimately created and the test failed for the wrong reason.
	var post HookSpec
	for _, h := range ClaudeHooks("sctx") {
		if h.Event == "PostToolUse" {
			post = h
		}
	}
	path := settingsAt(t, home, `{"hooks":{"PostToolUse":[{"matcher":"`+post.Matcher+`","hooks":[{"type":"command","command":"/usr/bin/theirs"}]}]}}`)
	if _, err := InstallHooks(home, "sctx"); err != nil {
		t.Fatal(err)
	}
	doc := readSettings(t, path)
	groups, _ := doc["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(groups) != 1 {
		t.Fatalf("created %d groups for one matcher", len(groups))
	}
	entries, _ := groups[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 2 {
		t.Fatalf("want the developer's hook AND ours, got %d entries", len(entries))
	}
	if cmd, _ := entries[0].(map[string]any)["command"].(string); cmd != "/usr/bin/theirs" {
		t.Errorf("the developer's hook was displaced or reordered: %q", cmd)
	}
}

// A settings file we cannot parse is one we must not write. Silently rewriting
// malformed JSON destroys whatever the developer was in the middle of.
func TestUnparseableSettingsAreRefusedNotRewritten(t *testing.T) {
	home := t.TempDir()
	broken := `{"hooks": {`
	path := settingsAt(t, home, broken)

	if _, err := InstallHooks(home, "sctx"); err == nil {
		t.Error("wrote into a settings file we could not parse")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != broken {
		t.Errorf("the broken file was modified:\n%s", raw)
	}
}

// A machine with no settings.json at all is the fresh-install case.
func TestAFreshMachineGetsEveryHook(t *testing.T) {
	home := t.TempDir()
	changed, err := InstallHooks(home, "sctx")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != len(ClaudeHooks("sctx")) {
		t.Fatalf("want every hook installed, got %v", changed)
	}
	_, states, err := InspectHooks(home, "sctx")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if !st.Installed {
			t.Errorf("%s(%s) not installed after install", st.Event, st.Matcher)
		}
	}
}

// The Bash hook is the savings engine, the PostToolUse hook is the memory
// delivery path, and the SessionStart and search hooks are the orientation
// path. They fail independently, so status has to name every one of them.
//
// Keyed on SUBCOMMAND rather than Event: two hooks now share the PreToolUse
// event (Bash, and Grep|Glob|Agent), so an Event-keyed map would let one of
// them overwrite the other's result and the assertion would depend on slice
// order.
func TestEveryHookIsTrackedSeparately(t *testing.T) {
	home := t.TempDir()
	settingsAt(t, home, theRealSettings)
	_, states, err := InspectHooks(home, "/Users/x/.local/bin/sctx")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != len(ClaudeHooks("sctx")) {
		t.Fatalf("want every hook tracked, got %d", len(states))
	}
	bySub := map[string]bool{}
	for _, st := range states {
		bySub[st.Subcommand] = st.Installed
	}
	if !bySub["claude"] {
		t.Error("the existing Bash hook was not detected")
	}
	for _, absent := range []string{"claude-post-tool", "claude-session-start", "claude-first-search"} {
		if bySub[absent] {
			t.Errorf("%s is absent from the fixture but was reported as installed", absent)
		}
	}
}

// THE regression from the end-to-end run. The path in an existing entry is
// whatever sctx was when the developer first ran setup — Homebrew, a dev build,
// a since-moved binary. Comparing it against the currently-running executable
// reported "not installed" and appended a SECOND Bash hook, so every command
// would have been wrapped twice. Unit tests passed because they used one path.
func TestAHookInstalledFromADifferentBinaryPathIsRecognised(t *testing.T) {
	home := t.TempDir()
	path := settingsAt(t, home, theRealSettings) // hook at /Users/x/.local/bin/sctx

	// setup now running from somewhere else entirely.
	changed, err := InstallHooks(home, "/opt/homebrew/bin/sctx")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changed {
		if strings.Contains(c, "PreToolUse(Bash)") {
			t.Errorf("installed a second Bash hook: %q", c)
		}
	}
	raw, _ := os.ReadFile(path)
	// Exactly one Bash hook: recognised by program name, not by binary path.
	if n := strings.Count(string(raw), "sctx hook claude\""); n != 1 {
		t.Errorf("want exactly 1 Bash hook, found %d:\n%s", n, raw)
	}
}

// `sctx hook claude` is a prefix of `sctx hook claude-post-tool`. A substring
// test would let the memory hook satisfy the Bash hook, and the savings engine
// would never be installed at all.
func TestTheMemoryHookDoesNotSatisfyTheBashHook(t *testing.T) {
	home := t.TempDir()
	settingsAt(t, home, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/x/sctx hook claude-post-tool"}]}]}}`)
	_, states, err := InspectHooks(home, "sctx")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.Event == "PreToolUse" && st.Installed {
			t.Error("claude-post-tool was accepted as the Bash rewrite hook")
		}
	}
}

func TestInvokesSctxHookIsWholeToken(t *testing.T) {
	for _, tc := range []struct {
		cmd, sub string
		want     bool
	}{
		{"/opt/homebrew/bin/sctx hook claude", "claude", true},
		{"/Users/x/.local/bin/sctx hook claude --fallback legacy-wrapper", "claude", true},
		{"sctx hook claude-post-tool", "claude-post-tool", true},
		{"/x/sctx hook claude-post-tool", "claude", false},
		{"/x/sctx hook claude", "claude-post-tool", false},
		{"/usr/bin/mysctx hook claude", "claude", false}, // suffix, not the program
		{"/x/sctx gain", "claude", false},
		// The new verbs, and the prefix pairs they create: "claude" is a prefix
		// of all three of the others, and "claude-session-start" shares none of
		// its tokens with them. A substring test would let any one satisfy any
		// other and setup would then report a green install it never made.
		{"sctx hook claude-session-start", "claude-session-start", true},
		{"sctx hook claude-first-search", "claude-first-search", true},
		{"/x/sctx hook claude-session-start", "claude", false},
		{"/x/sctx hook claude-first-search", "claude", false},
		{"/x/sctx hook claude", "claude-session-start", false},
		{"/x/sctx hook claude", "claude-first-search", false},
		{"/x/sctx hook claude-first-search", "claude-session-start", false},
		{"/x/sctx hook claude-post-tool", "claude-first-search", false},
		{"", "claude", false},
		// Windows: os.Executable() can return a path with a space
		// (`quoteBinaryForCommand` double-quotes it), so `strings.Fields` alone
		// would split the program token in two. splitCommandTokens must treat
		// the whole quoted run as one token before comparison.
		{`"C:\Users\Jane Doe\bin\sctx.exe" hook claude`, "claude", true},
		{`"C:\Users\Jane Doe\bin\sctx.exe" hook claude-post-tool`, "claude", false},
		{`"C:\Program Files\sctx\sctx.exe" hook claude`, "claude", true},
	} {
		if got := invokesSctxHook(tc.cmd, tc.sub); got != tc.want {
			t.Errorf("invokesSctxHook(%q, %q) = %v, want %v", tc.cmd, tc.sub, got, tc.want)
		}
	}
}

// The program TOKEN handed back for a doctor-style version comparison must be
// a bare, unquoted path — the caller passes it straight to exec, never
// through a shell — even though the settings.json entry embeds it quoted for
// the shell that DOES read it there.
func TestHookProgramTokenStripsQuotes(t *testing.T) {
	for _, tc := range []struct{ cmd, sub, want string }{
		{`"C:\Users\Jane Doe\bin\sctx.exe" hook claude`, "claude", `C:\Users\Jane Doe\bin\sctx.exe`},
		{"/opt/homebrew/bin/sctx hook claude", "claude", "/opt/homebrew/bin/sctx"},
	} {
		got, ok := hookProgramToken(tc.cmd, tc.sub)
		if !ok || got != tc.want {
			t.Errorf("hookProgramToken(%q, %q) = (%q, %v), want (%q, true)", tc.cmd, tc.sub, got, ok, tc.want)
		}
	}
}

// quoteBinaryForCommand is the other half: a path with a space is quoted for
// embedding in a hook COMMAND STRING, a path without one is left exactly as
// every non-Windows fixture already expects.
func TestQuoteBinaryForCommand(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`C:\Users\Jane Doe\bin\sctx.exe`, `"C:\Users\Jane Doe\bin\sctx.exe"`},
		{`/opt/homebrew/bin/sctx`, `/opt/homebrew/bin/sctx`},
		{`C:\sctx\sctx.exe`, `C:\sctx\sctx.exe`},
	} {
		if got := quoteBinaryForCommand(tc.in); got != tc.want {
			t.Errorf("quoteBinaryForCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHookBinaryParsesTheInstalledProgramToken(t *testing.T) {
	home := t.TempDir()
	settingsAt(t, home, theRealSettings)
	got, ok := HookBinary(home, claudeAgent())
	if !ok {
		t.Fatal("expected a hook binary to be found")
	}
	if got != "/Users/x/.local/bin/sctx" {
		t.Fatalf("got %q, want /Users/x/.local/bin/sctx", got)
	}
}

func TestHookBinaryMissingHookIsNotOK(t *testing.T) {
	home := t.TempDir()
	if _, ok := HookBinary(home, claudeAgent()); ok {
		t.Fatal("expected no hook binary on a machine with no settings.json")
	}
}
