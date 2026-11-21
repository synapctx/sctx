// Claude Code permission-rule awareness for `sctx hook claude`.
//
// WHY THIS EXISTS. Verified live against Claude Code 2.1.263 (2026-11-21): a
// PreToolUse hook that returns `updatedInput` without a permissionDecision makes
// Claude Code evaluate the user's permission rules against NEITHER the original
// nor a matching form of the rewritten command. With `permissions.deny:
// ["Bash(git status:*)"]`, the model's `git status` is hard-denied when the hook
// stays out of the way ("Permission to use Bash with command git status has been
// denied."), but when the hook rewrites it to `sctx git status` the deny rule no
// longer fires and the call degrades to "This command requires approval" — a
// guardrail written as DENY became a prompt the user can click through. The same
// holds for `ask` rules (the prompt shows the rewritten command, not the one the
// rule was written for).
//
// So before rewriting, the hook reads the deny/ask rules Claude Code would have
// applied to the ORIGINAL command and, when any could match, leaves the command
// untouched so Claude's own evaluation runs on the text the user wrote a rule
// for. It is deliberately OVER-inclusive: a spurious step-aside only costs one
// command's savings, a missed match silently weakens a guardrail. Anything
// unreadable steps aside too. Everything here is fail-open in the hook's sense
// (it never errors, never blocks) and never emits a permission decision.
package hook

import (
	"bytes"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// managedSettingsDirs lists the directories Claude Code reads managed policy
// from on goos.
func managedSettingsDirs(goos string) []string {
	dirs := []string{"/Library/Application Support/ClaudeCode", "/etc/claude-code"}
	if goos == "windows" {
		dirs = append(dirs, `C:\Program Files\ClaudeCode`)
	}
	return dirs
}

// managedSettingsFilesIn returns dir's managed-settings.json plus every
// managed-settings.d/*.json drop-in (sorted, so the order is stable).
func managedSettingsFilesIn(dir string) []string {
	files := []string{filepath.Join(dir, "managed-settings.json")}
	drop, _ := filepath.Glob(filepath.Join(dir, "managed-settings.d", "*.json"))
	sort.Strings(drop)
	return append(files, drop...)
}

// claudeSettingsFiles lists every settings file that can carry permission rules
// for a session in cwd: the user's (CLAUDE_CONFIG_DIR or ~/.claude), managed
// policy (file + drop-ins), and project + project-local files for cwd and every
// ancestor (Claude anchors the project where it was started, which the hook
// cannot see once the model has cd'd, so every ancestor is read —
// over-inclusive by design).
//
// KNOWN LIMIT: rules that arrive through `--settings`, `--disallowedTools`, MDM
// or server-managed policy never touch a file this hook can read, so they are
// invisible to it. A deny delivered that way can still be dodged by a rewrite.
func claudeSettingsFiles(home, cwd string) []string {
	var files []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" && home != "" {
		configDir = filepath.Join(home, ".claude")
	}
	if configDir != "" {
		add(filepath.Join(configDir, "settings.json"))
	}
	for _, dir := range managedSettingsDirs(runtime.GOOS) {
		for _, f := range managedSettingsFilesIn(dir) {
			add(f)
		}
	}
	addProject := func(dir string) {
		if dir == "" {
			return
		}
		add(filepath.Join(dir, ".claude", "settings.json"))
		add(filepath.Join(dir, ".claude", "settings.local.json"))
	}
	addProject(os.Getenv("CLAUDE_PROJECT_DIR"))
	for dir := cwd; dir != "" && dir != "."; {
		addProject(dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return files
}

// claudeSettingsDoc is the slice of settings.json this file cares about.
type claudeSettingsDoc struct {
	Permissions struct {
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
		Allow []string `json:"allow"`
	} `json:"permissions"`
}

// readClaudeSettings returns ok=false only when the file EXISTS but cannot be
// read or parsed — the caller must then assume it could hold any rule. A missing
// file is a normal absence and returns an empty doc with ok=true.
func readClaudeSettings(path string) (doc claudeSettingsDoc, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return doc, os.IsNotExist(err) || isNotDir(err)
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(raw)) == 0 {
		return doc, true
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return claudeSettingsDoc{}, false
	}
	return doc, true
}

// isNotDir reports ENOTDIR-style absence (a path component is a file).
func isNotDir(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not a directory")
}

// bashRuleContent parses one permission entry. isBash is false for other tools'
// rules; all is true for a bare `Bash` (or empty parens), which covers every
// command.
func bashRuleContent(entry string) (content string, all, isBash bool) {
	entry = strings.TrimSpace(entry)
	if entry == "Bash" {
		return "", true, true
	}
	if !strings.HasPrefix(entry, "Bash(") || !strings.HasSuffix(entry, ")") {
		return "", false, false
	}
	content = strings.TrimSpace(entry[len("Bash(") : len(entry)-1])
	if content == "" || content == "*" || content == ":*" {
		return "", true, true
	}
	return content, false, true
}

// ruleLiteral is the fixed text a rule demands before its first wildcard: the
// prefix of `git push:*`, the head of `git push *`, or the whole of an exact
// rule.
func ruleLiteral(content string) string {
	if i := strings.Index(content, "*"); i >= 0 {
		content = content[:i]
	}
	content = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(content), ":"))
	return strings.Join(strings.Fields(content), " ")
}

// bashRuleMayMatch reports whether a Bash rule's content could match cmd or any
// command segment of it. Over-inclusive on purpose (see file comment).
//
// Segments are split on the RAW command (newline, `;`, `&`, `|`, parens,
// backtick) before whitespace is normalised — normalising first folded `\n` into
// a space and hid segment starts. A glob is tried against the suffix starting at
// every token of every segment (also with that token basenamed), so a rule
// anchored at the command start still sees `ENV=x make deploy`, `timeout 5 make
// deploy` and `/usr/bin/make deploy`.
func bashRuleMayMatch(content, cmd string) bool {
	norm := strings.Join(strings.Fields(cmd), " ")
	lit, glued := ruleLiteral(content), starGluedToLiteral(content)
	if lit != "" && containsWord(norm, lit, !glued) {
		return true
	}
	if !strings.Contains(content, "*") {
		return false
	}
	pat := strings.TrimSuffix(content, ":*")
	if pat != content {
		pat += " *"
	}
	pat = strings.Join(strings.Fields(pat), " ")
	if globMatch(pat, norm) {
		return true
	}
	leadingStar := strings.HasPrefix(pat, "*")
	for _, raw := range strings.FieldsFunc(cmd, func(r rune) bool {
		return strings.ContainsRune(";&|\n\r()`", r)
	}) {
		seg := strings.Join(strings.Fields(raw), " ")
		if seg == "" {
			continue
		}
		if globMatch(pat, seg) {
			return true
		}
		if leadingStar {
			continue // `*` already swallowed every prefix; suffixes add nothing
		}
		// The command head sits within the first few tokens (assignments,
		// wrappers); the cap bounds the work on a huge heredoc.
		off := 0
		for n := 0; n < 64 && off < len(seg); n++ {
			rest := seg[off:]
			if globMatch(pat, rest) {
				return true
			}
			tokEnd := strings.IndexByte(rest, ' ')
			if tokEnd < 0 {
				tokEnd = len(rest)
			}
			if tok := rest[:tokEnd]; strings.ContainsAny(tok, `/\`) {
				if base := tok[strings.LastIndexAny(tok, `/\`)+1:]; base != "" && globMatch(pat, base+rest[tokEnd:]) {
					return true
				}
			}
			if tokEnd == len(rest) {
				break
			}
			off += tokEnd + 1
		}
	}
	return false
}

// starGluedToLiteral reports whether the first `*` of a rule directly follows
// its literal (`make deploy*`), as opposed to being a separate word (`make
// deploy *`) or the legacy `:*` prefix form. A glued star means the literal's
// last word may continue (`deploy-prod`), so only a LEADING boundary applies.
func starGluedToLiteral(content string) bool {
	i := strings.Index(content, "*")
	return i > 0 && content[i-1] != ' ' && content[i-1] != '\t' && content[i-1] != ':'
}

// containsWord reports whether needle occurs in hay starting on a word
// boundary — and, when trailing is true, ending on one — so `git push` matches
// `cd x && git push -f` and `/usr/bin/git push` but not `legit pusher`.
func containsWord(hay, needle string, trailing bool) bool {
	for from := 0; from <= len(hay)-len(needle); {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(needle)
		beforeOK := i == 0 || !isWordByte(hay[i-1])
		afterOK := !trailing || end == len(hay) || !isWordByte(hay[end]) || !isWordByte(needle[len(needle)-1])
		if beforeOK && afterOK {
			return true
		}
		from = i + 1
	}
	return false
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

// globMatch matches s against pattern where `*` is any (possibly empty) run.
func globMatch(pattern, s string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		switch {
		case px < len(pattern) && pattern[px] == '*':
			star, mark = px, sx
			px++
		case px < len(pattern) && pattern[px] == s[sx]:
			px++
			sx++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// permissionStepAside reports whether the Claude hook must leave cmd untouched,
// and why. It is true when:
//   - any readable deny/ask rule could match cmd (see the file comment);
//   - any existing settings file cannot be read or parsed;
//   - the session is in "auto" permission mode AND SCT__AUTO_MODE_STEP_ASIDE is
//     true. A hook-modified input can void auto mode's classifier review ("a hook
//     changed this call's input after auto mode reviewed it, so the review does
//     not cover what would run" — observed live 2026-11-21), so
//     the refused call costs one extra turn. It is opt-in, not the default: the
//     refusal was intermittent (most rewritten calls in the same auto-mode
//     session ran), the cost of a refusal is a retry rather than a safety loss
//     (deny/ask rules are still honoured below in every mode), and stepping
//     aside unconditionally would remove every saving for auto-mode users.
func permissionStepAside(cmd, permissionMode, cwd string) (reason string, aside bool) {
	if permissionMode == "auto" && autoModeStepAside() {
		return "auto permission mode", true
	}
	home, _ := os.UserHomeDir()
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	fileRule := ""
	for _, path := range claudeSettingsFiles(home, cwd) {
		doc, ok := readClaudeSettings(path)
		if !ok {
			return unreadableSettingsPrefix + path, true
		}
		for _, list := range [][]string{doc.Permissions.Deny, doc.Permissions.Ask} {
			for _, entry := range list {
				content, all, isBash := bashRuleContent(entry)
				if !isBash {
					if fileRule == "" && isFileToolRule(entry) {
						fileRule = "permission rule " + entry + " in " + path
					}
					continue
				}
				if all || bashRuleMayMatch(content, cmd) {
					return "permission rule " + entry + " in " + path, true
				}
			}
		}
	}
	// FAIL-SAFE, UNVERIFIED CLAUDE BEHAVIOUR: Claude Code may apply Read(...)/
	// Edit(...)/Write(...) deny/ask rules to Bash commands that read files
	// (`cat .env`). Whether it does, and how it matches, is not something we have
	// verified, and a rewrite to `sctx cat .env` could dodge such a rule. So any
	// such rule plus a file-reading program we wrap steps aside; the cost is one
	// command's saving.
	if fileRule != "" && usesFileReadingProgram(cmd) {
		return fileRule + " (command reads files)", true
	}
	return "", false
}

// unreadableSettingsPrefix starts the step-aside reason for a settings file that
// exists but cannot be read or parsed; recordStepAside keys on it.
const unreadableSettingsPrefix = "unreadable settings file "

// isFileToolRule reports whether a permission entry is a Read/Edit/Write rule.
func isFileToolRule(entry string) bool {
	entry = strings.TrimSpace(entry)
	for _, tool := range []string{"Read", "Edit", "Write"} {
		if entry == tool || strings.HasPrefix(entry, tool+"(") {
			return true
		}
	}
	return false
}

// fileReadingPrograms are the programs sctx wraps whose job is reading or
// listing files.
var fileReadingPrograms = map[string]bool{
	"cat": true, "head": true, "tail": true, "grep": true, "rg": true, "sed": true,
	"ls": true, "find": true, "tree": true, "diff": true, "less": true, "more": true,
	"jq": true, "gofmt": true, "sqlite3": true,
}

// gitFileReadingSubcommands are the git subcommands that print file content
// (`git show HEAD:.env`) rather than repository metadata.
var gitFileReadingSubcommands = map[string]bool{
	"show": true, "diff": true, "grep": true, "blame": true, "cat-file": true,
}

// usesFileReadingProgram reports whether cmd runs a file-reading program as the
// effective program of a segment (past assignments and process wrappers). A
// segment fed by a pipe reads its stdin, not a file, so `go test | tail -5` does
// not count: judging every WORD made one common `Read(./.env)` rule cost the
// saving on every piped command. A command the segment scanner cannot read
// falls back to the over-inclusive word scan (fail-safe; the rewrite declines
// such commands anyway).
func usesFileReadingProgram(cmd string) bool {
	segs, ok := splitSegments(cmd)
	if !ok {
		return anyWordReadsFiles(cmd)
	}
	for _, seg := range segs {
		if seg.pipeFrom {
			continue
		}
		eff, _ := effectiveSegment(seg.text)
		fields := strings.Fields(eff)
		if len(fields) == 0 {
			continue
		}
		name := filepathBase(shellTokenValue(fields[0]))
		if fileReadingPrograms[name] {
			return true
		}
		if name == "git" {
			for _, f := range fields[1:] {
				if strings.HasPrefix(f, "-") {
					continue // global option such as -C dir is followed by its value; over-inclusive is fine
				}
				if gitFileReadingSubcommands[shellTokenValue(f)] {
					return true
				}
			}
		}
	}
	return false
}

// anyWordReadsFiles is the over-inclusive fallback: any word of cmd (split on
// whitespace, shell separators and quotes, then basenamed) names a file-reading
// program.
func anyWordReadsFiles(cmd string) bool {
	words := strings.FieldsFunc(cmd, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n;&|()`'\"<>=", r)
	})
	for _, w := range words {
		if i := strings.LastIndexAny(w, `/\`); i >= 0 {
			w = w[i+1:]
		}
		if fileReadingPrograms[w] {
			return true
		}
	}
	return false
}

// stepAsideNoteFile is the spool file holding the latest settings-file fault
// the hook stepped aside for.
const stepAsideNoteFile = "permission-stepaside"

// stepAsideNoteTTL bounds how long a recorded fault is surfaced.
const stepAsideNoteTTL = 7 * 24 * time.Hour

// recordStepAside persists (latest wins, atomic, 0600) why the hook stepped
// aside, when the reason is an unreadable/malformed settings file — the one
// reason the user can fix and cannot otherwise see, since hook stdout/stderr
// must stay clean. Best-effort: any failure is ignored.
func recordStepAside(reason string) {
	if !strings.HasPrefix(reason, unreadableSettingsPrefix) {
		return
	}
	dir := spoolDir()
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, stepAsideNoteFile+".*.tmp")
	if err != nil {
		return
	}
	line := time.Now().UTC().Format(time.RFC3339) + "\t" + strings.ReplaceAll(reason, "\n", " ") + "\n"
	_, werr := tmp.WriteString(line)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil ||
		os.Rename(tmp.Name(), filepath.Join(dir, stepAsideNoteFile)) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// ClaudeStepAsideNote returns the latest recorded settings-file fault when it is
// under seven days old at now. Used by `sctx setup`.
func ClaudeStepAsideNote(now time.Time) (string, bool) {
	dir := spoolDir()
	if dir == "" {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, stepAsideNoteFile))
	if err != nil {
		return "", false
	}
	ts, reason, ok := strings.Cut(strings.TrimSpace(string(raw)), "\t")
	at, perr := time.Parse(time.RFC3339, ts)
	if !ok || perr != nil || reason == "" || now.Sub(at) > stepAsideNoteTTL {
		return "", false
	}
	return reason, true
}

// ClaudeAllowRuleWarnings returns one human-readable warning per Claude Code
// allow rule that would auto-approve every sctx-wrapped command — `Bash(sctx:*)`,
// `Bash(sctx *)`, `Bash(sctx*)`. Such a rule pre-approves `sctx git push`,
// `sctx rm ...`, and anything else the hook wraps, regardless of what the user
// meant to allow or deny for the underlying command. (Live check on 2.1.263
// could not reproduce the auto-allow under `-p`, so this is a precaution, not a
// confirmed bypass.) Used by `sctx doctor` and `sctx setup`.
func ClaudeAllowRuleWarnings(home, cwd string) []string {
	var warnings []string
	for _, path := range claudeSettingsFiles(home, cwd) {
		doc, ok := readClaudeSettings(path)
		if !ok {
			continue
		}
		for _, entry := range doc.Permissions.Allow {
			content, all, isBash := bashRuleContent(entry)
			if !isBash || all || !strings.Contains(content, "*") {
				continue
			}
			if ruleLiteral(content) == "sctx" {
				warnings = append(warnings, path+": allow rule "+entry+
					" auto-approves every command sctx wraps (including git push); narrow it or remove it")
			}
		}
	}
	return warnings
}

// autoModeStepAside reports whether SCT__AUTO_MODE_STEP_ASIDE opts the Claude
// hook out of rewriting in auto permission mode (see permissionStepAside).
func autoModeStepAside() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SCT__AUTO_MODE_STEP_ASIDE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
