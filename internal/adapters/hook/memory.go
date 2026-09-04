package hook

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
	"github.com/synapctx/sctx/internal/platform/httpclient"
)

// RunClaudePostTool implements `sctx hook claude-post-tool`: a Claude Code
// PostToolUse hook on Edit/Write that puts what the organization already knows
// about a file in front of the agent that is editing it.
//
// This is the memory half of decision 0006, and it exists because recall cannot
// be earned the way search can. **No agent thinks "I should recall".** Search at
// least competes with grep for a job the agent knows it has; recall competes
// with nothing, because the agent does not know the question exists. Measured:
// 329 recalls, ever, against 418 memories written. The asset behind it is the
// most defensible thing we have and it was effectively write-only.
//
// So memory is DELIVERED, not discovered — at the one moment it is provably
// relevant, which is when someone edits the file it is about.
//
// Four properties keep an unprompted feature from becoming an unwelcome one:
//
//   - **PostToolUse, never PreToolUse.** The edit has already happened; this can
//     only add a note, never block or alter a write. A hook that can break an
//     edit is a hook a developer removes after the first false positive.
//   - **Fail open, always exit 0.** Any error — no key, no network, malformed
//     payload, a slow server — prints nothing and exits 0, which the hook
//     contract treats as "no decision, proceed normally".
//   - **Hard latency ceiling.** This sits in the edit loop. Late is worse than
//     absent, so the budget is short and expiring it is a normal outcome.
//   - **Silence is the common case and the correct one.** The server returns
//     notes only when a memory actually names the file; most edits get nothing.
const postToolBudget = 2800 * time.Millisecond

// surfacePath is the proxy endpoint. It lives on the telemetry ingest host
// because it shares that audience — an authenticated CLI on a developer's
// machine — rather than the MCP host, which serves agents that chose to ask.
const (
	surfacePath       = "/v1/surface/for-file"
	surfaceSymbolPath = "/v1/surface/for-symbol"
)

type postToolCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	CWD       string         `json:"cwd"`
	// SessionID is the Claude Code session, forwarded to the proxy's for-file/
	// for-symbol surface endpoints as sessionId so their usage events are
	// attributable to a session instead of arriving with no tool and no
	// session id at all (see developer-mcp-proxy's CallMeta stamping).
	SessionID string `json:"session_id"`
}

// surfaceNote is one note as the wire doc's extended shape carries it:
// kind/createdAt/verifiedAt/staleness/bound/id alongside the text, so the
// full-mode render can label it `[kind · date · verified|unverified · stale]`.
type surfaceNote struct {
	Text       string `json:"text"`
	CreatedAt  string `json:"createdAt"`
	VerifiedAt string `json:"verifiedAt"`
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Staleness  string `json:"staleness"`
	Bound      bool   `json:"bound"`
}

// forFileResponse is the proxy's /v1/surface/for-file answer. Mode is
// EMPTY, not defaulted here, when an old proxy predates it — the caller
// (memoryContext) is what treats an absent mode as "full", per the wire
// doc's additive rule ("an old proxy ignores unknown fields and returns no
// mode; the client treats absent mode as full").
type forFileResponse struct {
	Mode    string `json:"mode"`
	Pointer *struct {
		Count  int    `json:"count"`
		Newest string `json:"newest"`
	} `json:"pointer"`
	Notes []surfaceNote `json:"notes"`
}

// sessionStateWire is exactly the sessionState object the wire doc defines,
// built from this session's persisted offerState (offerstate.go).
type sessionStateWire struct {
	LastOfferedAt string `json:"lastOfferedAt"`
	NewestStamp   string `json:"newestStamp"`
	FullOffers    int    `json:"fullOffers"`
	PointerShown  bool   `json:"pointerShown"`
}

// forFileRequest is the extended /v1/surface/for-file request body: the
// existing fields plus sessionId, sessionState and symbols, all field names
// verbatim from the wire doc.
type forFileRequest struct {
	RepositoryName string           `json:"repositoryName"`
	FilePath       string           `json:"filePath"`
	SessionID      string           `json:"sessionId"`
	SessionState   sessionStateWire `json:"sessionState"`
	Symbols        []string         `json:"symbols,omitempty"`
}

// symbolEditRequest is the /v1/surface/for-symbol request in its "edit" mode
// (blast radius): the file that changed and the exported declaration names
// that changed in it, per the wire doc.
type symbolEditRequest struct {
	RepositoryName string   `json:"repositoryName"`
	FilePath       string   `json:"filePath"`
	Mode           string   `json:"mode"`
	Symbols        []string `json:"symbols"`
	SessionID      string   `json:"sessionId"`
}

// symbolEditResponse is the /v1/surface/for-symbol "edit" mode answer: only
// symbols with references in OTHER repositories are present at all.
type symbolEditResponse struct {
	Symbols []struct {
		Name              string   `json:"name"`
		SymbolPath        string   `json:"symbolPath"`
		OtherRepositories []string `json:"otherRepositories"`
		References        int      `json:"references"`
		Ambiguous         int      `json:"ambiguous"`
		Call              string   `json:"call"`
	} `json:"symbols"`
}

// identifierRE finds bare identifiers in an edit's new_string, for the
// "symbols" field the wire doc sends on for-file (≤12, for symbol-binding
// preference server-side). Single-character tokens are skipped: they carry
// no binding signal and would crowd out the cap with noise like loop
// variables.
var identifierRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// identifiersInText returns up to capN distinct identifiers from s, in the
// order they first appear.
func identifiersInText(s string, capN int) []string {
	if s == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range identifierRE.FindAllString(s, -1) {
		if len(m) < 2 || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) >= capN {
			break
		}
	}
	return out
}

// hookOutput is the PostToolUse JSON contract: additionalContext is placed next
// to the tool result, where the agent reads it.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// RunClaudePostTool always returns 0. There is no failure mode worth reporting
// to an agent mid-edit. version is rendered into the User-Agent header of the
// surface calls it makes.
func RunClaudePostTool(in io.Reader, out io.Writer, cfg config.Config, version string) int {
	if os.Getenv("SCT__MEMORY_SURFACING_DISABLED") == "true" {
		return 0
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return 0
	}
	var call postToolCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}

	if call.ToolName == "Bash" {
		return runClaudePostToolBash(out, cfg, version, call)
	}

	switch call.ToolName {
	case "Edit", "Write", "NotebookEdit", "MultiEdit":
		absPath, _ := call.ToolInput["file_path"].(string)
		if absPath == "" {
			return 0
		}
		root, repo, rel := repoRootAndRelativePath(absPath, call.CWD)
		if rel == "" {
			return 0
		}
		return runClaudePostToolEdit(out, cfg, version, call, root, repo, rel)
	default:
		return 0
	}
}

// runClaudePostToolEdit is the Edit/Write/NotebookEdit/MultiEdit branch of
// PostToolUse: it can make up to two surface calls, for-file (memory bound
// to the edited file) and for-symbol in "edit" mode (blast radius on any
// EXPORTED Go declaration the edit just changed) — run CONCURRENTLY under
// one deadline (postToolBudget), per the wire doc: "Both hook calls run
// concurrently under ONE deadline". Either call may be skipped entirely
// before it ever reaches the network: for-file when the same file was
// offered <60s ago this session (offerState.debounced), for-symbol when
// there are no changed exported declarations or every one of them was
// already asked about within the last 10 minutes.
func runClaudePostToolEdit(out io.Writer, cfg config.Config, version string, call postToolCall, root, repo, rel string) int {
	// No key means no organization to ask, and there is nothing useful to say
	// about that here — `sctx setup` and `sctx gain` own that conversation.
	token, _ := cfg.TokenForOrg(orgOf(repo))
	if token == "" {
		return 0
	}

	id := sanitizeSessionID(call.SessionID)
	state := loadOfferState(id)

	oldStr, _ := call.ToolInput["old_string"].(string)
	newStr, _ := call.ToolInput["new_string"].(string)
	symbols := identifiersInText(newStr, 12)

	var askSymbols []string
	for _, name := range changedExportedSymbols(rel, oldStr, newStr) {
		if !state.symbolRecentlyAsked(name) {
			askSymbols = append(askSymbols, name)
		}
	}
	if len(askSymbols) > 3 {
		askSymbols = askSymbols[:3]
	}

	ctx, cancel := context.WithTimeout(context.Background(), postToolBudget)
	defer cancel()

	var wg sync.WaitGroup
	callFile := !state.debounced(rel)

	var fileResp forFileResponse
	fileOK := false
	if callFile {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := fetchForFile(ctx, cfg, token, repo, rel, call.SessionID, version, state.sessionStateFor(rel), symbols)
			if err == nil {
				fileResp, fileOK = resp, true
			}
		}()
	}

	var symResp symbolEditResponse
	symOK := false
	if len(askSymbols) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := fetchSymbolEdit(ctx, cfg, token, repo, rel, askSymbols, call.SessionID, version)
			if err == nil {
				symResp, symOK = resp, true
			}
		}()
	}
	wg.Wait()

	var lines []string
	if fileOK {
		if body := memoryContext(rel, fileResp); body != "" {
			lines = append(lines, body)
		}
		state.noteFileOffer(rel, fileResp, time.Now())
	}
	if symOK {
		for _, name := range askSymbols {
			state.markSymbolAsked(name)
		}
		home, _ := os.UserHomeDir()
		server := claudeServerNameFor(home, call.CWD, root, token)
		guessed := server == ""
		if guessed {
			server = orgOf(repo)
		}
		if body := symbolEditContext(server, guessed, symResp); body != "" {
			lines = append(lines, body)
		}
	}
	saveOfferState(id, state)

	if len(lines) == 0 {
		return 0
	}
	writeAdditionalContext(out, "PostToolUse", strings.Join(lines, "\n\n"))
	return 0
}

// runClaudePostToolBash handles the Bash branch of PostToolUse. Two
// independent signals about the same just-run command are computed and
// combined into ONE additionalContext body rather than one pre-empting the
// other:
//
//   - repeatedRunNudge (roadmap item 4, 2026-09-04): local-only, no API key
//     needed, reads this machine's own stats.db.
//   - the cross-repository call-site nudge (roadmap item 0b): needs an org
//     key and a network call, and only applies when the command was a
//     genuine symbol search a plain grep could not see past.
func runClaudePostToolBash(out io.Writer, cfg config.Config, version string, call postToolCall) int {
	cmd, _ := call.ToolInput["command"].(string)

	var lines []string
	if line := repeatedRunNudge(cfg, call.SessionID, cmd); line != "" {
		lines = append(lines, line)
	}

	// Every rejection here happens BEFORE any network call, because the
	// cheapest nudge is the one we decide not to make — and this fires on
	// every Bash command the agent runs.
	if sym := grepSymbol(cmd); sym != "" {
		if repo := gitrepo.Detect(call.CWD); repo != "" {
			// No key means no organization to ask, and there is nothing
			// useful to say about that here — `sctx setup`/`sctx gain` own
			// that conversation.
			if token, _ := cfg.TokenForOrg(orgOf(repo)); token != "" {
				if body := symbolContext(sym, fetchElsewhere(cfg, token, repo, sym, call.SessionID, version)); body != "" {
					lines = append(lines, body)
				}
			}
		}
	}

	if len(lines) == 0 {
		return 0
	}
	writeAdditionalContext(out, "PostToolUse", strings.Join(lines, "\n\n"))
	return 0
}

// repoRootAndRelativePath turns the absolute path the hook was given into the
// repository root, its (org/repo) name and the server-indexed relative path.
//
// Relative to the REPOSITORY ROOT, not to the agent's working directory: an
// agent's cwd wanders during a session, and a path resolved against it would
// silently stop matching anything the moment it changed. The root is
// returned too, alongside the name, because claudeServerNameFor needs it to
// find a project-scope `.mcp.json` (see sessionstart.go / claudeservers.go).
func repoRootAndRelativePath(absPath, cwd string) (root, repo, rel string) {
	dir := filepath.Dir(absPath)
	root, name, ok := gitrepo.RootAndName(dir)
	if !ok {
		// Fall back to the agent's cwd only to name the repository; without a
		// root there is no relative path to compute, so we stop.
		_ = cwd
		return "", "", ""
	}
	r, err := filepath.Rel(root, absPath)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", "", ""
	}
	return root, name, filepath.ToSlash(r)
}

func orgOf(repoFullName string) string {
	if i := strings.IndexByte(repoFullName, '/'); i > 0 {
		return repoFullName[:i]
	}
	return ""
}

// fetchForFile calls /v1/surface/for-file with the wire doc's extended
// request shape, under ctx — a caller-supplied, already-running deadline, so
// this and fetchSymbolEdit share exactly ONE budget when run concurrently.
func fetchForFile(ctx context.Context, cfg config.Config, token, repo, rel, sessionID, version string, state sessionStateWire, symbols []string) (forFileResponse, error) {
	var out forFileResponse
	err := postSurface(ctx, cfg, token, surfacePath, forFileRequest{
		RepositoryName: repo,
		FilePath:       rel,
		SessionID:      sessionID,
		SessionState:   state,
		Symbols:        symbols,
	}, &out, version)
	return out, err
}

// fetchSymbolEdit calls /v1/surface/for-symbol in "edit" mode (blast
// radius): the names diffed by exported.go, scoped to the file they changed
// in.
func fetchSymbolEdit(ctx context.Context, cfg config.Config, token, repo, rel string, symbols []string, sessionID, version string) (symbolEditResponse, error) {
	var out symbolEditResponse
	err := postSurface(ctx, cfg, token, surfaceSymbolPath, symbolEditRequest{
		RepositoryName: repo,
		FilePath:       rel,
		Mode:           "edit",
		Symbols:        symbols,
		SessionID:      sessionID,
	}, &out, version)
	return out, err
}

type elsewhereResult struct {
	Elsewhere    int      `json:"elsewhere"`
	Repositories []string `json:"repositories"`
	Ambiguous    int      `json:"ambiguous"`
}

// fetchElsewhere asks for the call sites the developer's grep could not see.
// It owns its own deadline: unlike the Edit/Write branch, the Bash branch
// never runs this alongside another network call.
func fetchElsewhere(cfg config.Config, token, repo, symbol, sessionID, version string) elsewhereResult {
	var out elsewhereResult
	ctx, cancel := context.WithTimeout(context.Background(), postToolBudget)
	defer cancel()
	_ = postSurface(ctx, cfg, token, surfaceSymbolPath,
		map[string]string{"repositoryName": repo, "symbol": symbol, "sessionId": sessionID}, &out, version)
	return out
}

// postSurface is the one HTTP shape every surface lookup shares. Every
// failure is silence: sctx reads any error as "no suggestion", and an error
// rendered into an agent's context would be pure noise. version is rendered
// into the request's User-Agent header. ctx carries the caller's own
// deadline, so two calls made concurrently (runClaudePostToolEdit) share one
// budget rather than each getting a fresh postToolBudget.
func postSurface(ctx context.Context, cfg config.Config, token, path string, payload any, into any, version string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, surfaceEndpointFor(cfg.TelemetryEndpoint, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", httpclient.UserAgent(version, "claude-code"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("surface: %s", resp.Status)
	}
	return json.UnmarshalDecode(jsontext.NewDecoder(io.LimitReader(resp.Body, 64<<10)), into)
}

// surfaceEndpoint derives the surface URL from the configured telemetry
// endpoint, so one `sctx init` configures both and they can never point at
// different deployments.
func surfaceEndpointFor(telemetryEndpoint, path string) string {
	if i := strings.Index(telemetryEndpoint, "/v1/"); i > 0 {
		return telemetryEndpoint[:i] + path
	}
	return strings.TrimRight(telemetryEndpoint, "/") + path
}

// writeAdditionalContext emits the PostToolUse JSON the agent's host reads.
//
// The wording states WHERE this came from and that it was not asked for, because
// an unattributed paragraph appearing next to a tool result is indistinguishable
// from the agent's own reasoning — and a memory is a claim by a teammate, which
// the agent should weigh as such rather than absorb as fact.
// memoryContext renders what the organization knows about a file, per the
// wire doc's three modes. An EMPTY resp.Mode is an old proxy that predates
// this feature, and is treated as "full" — today's behaviour, unchanged.
func memoryContext(rel string, resp forFileResponse) string {
	mode := resp.Mode
	if mode == "" {
		mode = "full"
	}
	switch mode {
	case "silent":
		return ""
	case "pointer":
		if resp.Pointer == nil || resp.Pointer.Count == 0 {
			return ""
		}
		return fmt.Sprintf("SynapCTX: %d notes on %s, newest %s — recall_memory",
			resp.Pointer.Count, rel, dateOnly(resp.Pointer.Newest))
	default: // "full"
		notes := resp.Notes
		if len(notes) > 2 {
			notes = notes[:2]
		}
		if len(notes) == 0 {
			return ""
		}
		var b strings.Builder
		fmt.Fprintf(&b, "SynapCTX organizational memory about %s — recorded by a teammate's agent, surfaced because you edited this file (nobody asked for it):\n", rel)
		for _, n := range notes {
			fmt.Fprintf(&b, "\n• [%s] %s\n", noteLabel(n), truncateRunes(n.Text, 280))
		}
		b.WriteString("\nUse recall_memory for the full text, or store_memory if you learn something here that the next person will need.")
		return b.String()
	}
}

// noteLabel renders the `kind · date · verified|unverified · stale?` tag the
// wire doc specifies for a full-mode note.
func noteLabel(n surfaceNote) string {
	var parts []string
	if n.Kind != "" {
		parts = append(parts, n.Kind)
	}
	if d := dateOnly(n.CreatedAt); d != "" {
		parts = append(parts, d)
	}
	if n.VerifiedAt != "" {
		parts = append(parts, "verified")
	} else {
		parts = append(parts, "unverified")
	}
	if n.Staleness != "" {
		parts = append(parts, n.Staleness)
	}
	return strings.Join(parts, " · ")
}

// symbolEditContext renders the blast-radius line for the for-symbol "edit"
// mode response: one line per symbol that has references in OTHER
// repositories, naming the repositories, the count and the EXACT
// find_references call — in THIS MACHINE'S own tool namespace
// (claudeServerNameFor), not whatever name the proxy guessed, for the same
// reason toolName() exists on the session brief: a tool name an agent cannot
// call is worse than no suggestion.
func symbolEditContext(server string, guessed bool, resp symbolEditResponse) string {
	var lines []string
	for _, s := range resp.Symbols {
		if len(s.OtherRepositories) == 0 || s.SymbolPath == "" {
			continue
		}
		call := fmt.Sprintf("%s {\"symbol_path\": %q}", toolName(server, "find_references", guessed), s.SymbolPath)
		repos := strings.Join(s.OtherRepositories, ", ")
		switch {
		case s.Ambiguous <= 0:
			lines = append(lines, fmt.Sprintf("SynapCTX: %s is used in %s (%d references) — %s",
				s.Name, repos, s.References, call))
		case s.Ambiguous >= s.References:
			lines = append(lines, fmt.Sprintf("SynapCTX: %s may be used in %s (%d name-only matches, receiver unresolved) — %s",
				s.Name, repos, s.References, call))
		default:
			lines = append(lines, fmt.Sprintf("SynapCTX: %s is used in %s (%d references, %d name-only) — %s",
				s.Name, repos, s.References, s.Ambiguous, call))
		}
	}
	return strings.Join(lines, "\n")
}

// symbolContext renders ONLY what the grep could not have seen.
//
// It states the count, names the repositories and names the tool — a suggestion
// an agent cannot act on is an advert. It does not repeat what the grep found,
// because that answer is already on screen and correct.
func symbolContext(symbol string, e elsewhereResult) string {
	if e.Elsewhere == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SynapCTX: your search covered this checkout. %s also has %d call site(s) in %s",
		symbol, e.Elsewhere, strings.Join(e.Repositories, ", "))
	if e.Ambiguous > 0 {
		fmt.Fprintf(&b, ", plus %d name-only match(es) whose receiver could not be resolved", e.Ambiguous)
	}
	b.WriteString(".\nRun find_references for the exhaustive list before changing its signature.")
	return b.String()
}

// event is a parameter rather than a constant because more than one Claude Code
// event now carries additionalContext, and the host DISCARDS an envelope whose
// hookEventName does not match the event it fired — silently, which is the worst
// possible failure for a hook whose whole job is to add a line.
func writeAdditionalContext(out io.Writer, event, body string) {
	var o hookOutput
	o.HookSpecificOutput.HookEventName = event
	o.HookSpecificOutput.AdditionalContext = body
	_ = json.MarshalEncode(jsontext.NewEncoder(out), o)
}
