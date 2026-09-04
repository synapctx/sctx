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
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
	"github.com/synapctx/sctx/internal/platform/httpclient"
)

// RunClaudeSessionStart implements `sctx hook claude-session-start`: a Claude
// Code SessionStart hook that opens a session with what the organization
// already knows about the repository the developer is standing in.
//
// It is the ORIENTATION half of the same problem the PostToolUse hook solves
// mid-edit. That hook fires when a memory is provably relevant to one file;
// this one fires before the agent has read anything, when the cheapest possible
// intervention — naming the tools, in the exact namespace this machine uses —
// still changes which tool the agent reaches for first. Nothing competes for
// that moment, which is why it is worth spending a network call on.
//
// The contract differs from the other hooks and the difference is load-bearing:
// SessionStart injects PLAIN STDOUT as model context, with no JSON envelope. A
// hookSpecificOutput object printed here would be shown to the model verbatim,
// as JSON, which is why nothing in this file reuses writeAdditionalContext.
//
// Fail open in every branch: no key, no repository, no network, a 500, a
// malformed payload — print nothing, exit 0. A session that starts slowly or
// noisily because of us is a hook the developer removes.
// 4500ms, not 3000: the proxy answers the brief under its own 3000ms budget
// and the first recall in a window can spend most of it on an embedding
// attempt before falling back to keyword recall (measured 2026-09-03: 2.9s
// with the embeddings provider's circuit open, ~0.3s otherwise). A hook budget
// EQUAL to the server's cannot succeed on that path — it times out with the
// answer already on the wire — so this leaves room for the round trip.
const sessionStartBudget = 4500 * time.Millisecond

// sessionStartPath is the per-repository brief: org memory bound to this
// repository plus what the index currently holds for it.
const sessionStartPath = "/v1/surface/for-repo"

// Notes here are ORIENTATION, not a mid-edit interruption, so they are allowed
// to be much fuller than the 480-char notes memory.go surfaces next to a tool
// result — the agent has nothing else in context yet and is reading, not
// mid-task. Six is where a brief stops being read as a brief.
const (
	sessionNoteMaxChars = 1400
	sessionNoteMax      = 6
)

type sessionStartCall struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Source    string `json:"source"` // startup | resume | clear | compact
}

// forRepoResponse mirrors the proxy's brief. EVERY field is optional: the
// server predates none of this and a partially-populated brief is still worth
// printing, so nothing here may be treated as required.
type forRepoResponse struct {
	Organization string `json:"organization"`
	Repository   struct {
		Name           string `json:"name"`
		IndexingStatus string `json:"indexingStatus"`
		Refs           []struct {
			Name             string `json:"name"`
			Role             string `json:"role"` // "primary" | anything else
			IndexedCommitSha string `json:"indexedCommitSha"`
			IndexedAt        string `json:"indexedAt"`
		} `json:"refs"`
	} `json:"repository"`
	// RetrievalHint is one concrete retrieve_context call the proxy already
	// knows how to phrase for this repository — printed verbatim so a session
	// that opens with recall_memory still sees the retrieval it is skipping.
	// Empty when the repository did not resolve; older proxies simply omit
	// the field, which decodes to the zero value here.
	RetrievalHint string `json:"retrievalHint"`
	// BootstrapNote is shown at most once per session (the proxy's own
	// NudgeLedger gate) — a fact about the memory system itself, not about
	// this repository, so it is worth one line the first time and noise
	// after. Absent from an older proxy decodes to the zero value.
	BootstrapNote string `json:"bootstrapNote"`
	Notes         []struct {
		ID           string   `json:"id"`
		Kind         string   `json:"kind"`
		CreatedAt    string   `json:"createdAt"`
		Text         string   `json:"text"`
		Repositories []string `json:"repositories"`
	} `json:"notes"`
}

// RunClaudeSessionStart always returns 0. See the type comment: there is no
// failure here worth delaying a session for.
func RunClaudeSessionStart(in io.Reader, out io.Writer, cfg config.Config, version string) int {
	if os.Getenv("SCT__MEMORY_SURFACING_DISABLED") == "true" {
		return 0
	}
	// Bounded like every other hook: stdin is whatever the client sends, and an
	// unbounded read here would be an unbounded allocation in the session's
	// critical path.
	data, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return 0
	}
	var call sessionStartCall
	if err := json.Unmarshal(data, &call); err != nil {
		return 0
	}

	// Root and name in one walk: the root is needed twice over — to read local
	// HEAD, and to find a project-scope `.mcp.json` naming the MCP server.
	root, repo, _ := gitrepo.RootAndName(call.CWD)
	if repo == "" {
		// Not inside a repository at all: a workspace root holding several
		// checkouts side by side (plan item B5) gets an org-level brief
		// instead of the per-repository one below.
		return runClaudeWorkspaceBrief(out, cfg, call, version)
	}
	org := orgOf(repo)
	token, _ := cfg.TokenForOrg(org)
	if token == "" {
		// No key means no organization to ask. `sctx setup` owns that
		// conversation; saying it here would put a configuration complaint in
		// front of an agent that cannot act on it.
		return 0
	}

	home, _ := os.UserHomeDir()
	server := claudeServerNameFor(home, call.CWD, root, token)
	// `guessed` is tracked separately rather than inferred from `server == org`,
	// because on the machine this feature was built for the registered name IS
	// the org slug ("parlitrack", not "synapctx-parlitrack") — the one case where
	// that inference would hedge a name we read straight out of the config.
	guessed := server == ""
	if guessed {
		// The org slug is the best guess and is right on a machine that took the
		// documented server name. The prose form hedges (see toolName) so a wrong
		// guess reads as a description rather than a tool name to copy.
		server = org
	}

	// A compaction is not a new session: the agent has been working here for a
	// while and the repository facts have not changed. It gets a reminder that
	// costs nothing and NO network call — compaction happens under context
	// pressure, which is the worst moment to spend three seconds.
	if call.Source == "compact" {
		fmt.Fprintf(out, "SynapCTX (org %s) is still one call away: %s and %s answer what this session just lost.\n",
			org, toolName(server, "recall_memory", guessed), toolName(server, "retrieve_context", guessed))
		fmt.Fprintln(out, "Anything decided in this session belongs in store_memory before it is compacted away again.")
		return 0
	}

	brief, err := fetchRepoBrief(cfg, token, repo, call, version)
	if err != nil {
		return 0
	}
	localSha, localBranch := localHead(root)
	if text := renderSessionBrief(repo, server, guessed, brief, localSha, localBranch); text != "" {
		fmt.Fprint(out, text)
	}
	return 0
}

func fetchRepoBrief(cfg config.Config, token, repo string, call sessionStartCall, version string) (forRepoResponse, error) {
	var out forRepoResponse
	payload := map[string]string{
		"repositoryName": repo,
		"sessionId":      call.SessionID,
		"source":         call.Source,
		"client":         "claude-code",
		"sctxVersion":    version,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return out, err
	}
	// Deliberately longer than postToolBudget: session start tolerates more
	// because NOTHING is waiting on it yet. The mid-edit hook sits between an
	// agent and a tool result it already has; this one runs while the developer
	// is still typing their first request.
	ctx, cancel := context.WithTimeout(context.Background(), sessionStartBudget)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		surfaceEndpointFor(cfg.TelemetryEndpoint, sessionStartPath), bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", httpclient.UserAgent(version, "claude-code"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("surface: %s", resp.Status)
	}
	// Larger than the mid-edit cap because the notes are allowed to be fuller.
	if err := json.UnmarshalDecode(jsontext.NewDecoder(io.LimitReader(resp.Body, 256<<10)), &out); err != nil {
		return out, err
	}
	return out, nil
}

// toolName renders one tool in the namespace this machine actually uses. When
// the server name was GUESSED rather than read from `.claude.json`, it is
// phrased as prose ("the server for org X") instead of a literal tool name: a
// name an agent can copy and call must be one that exists.
func toolName(server, tool string, guessed bool) string {
	if guessed {
		return fmt.Sprintf("mcp__<server for org %s>__%s", server, tool)
	}
	return fmt.Sprintf("mcp__%s__%s", server, tool)
}

// renderSessionBrief builds the plain-text brief. Every line is dropped when
// the data behind it is absent rather than printed with a placeholder — a brief
// full of "unknown" teaches the agent to skim the whole thing.
func renderSessionBrief(repo, server string, guessed bool, brief forRepoResponse, localSha, localBranch string) string {
	name := repo
	if brief.Repository.Name != "" {
		name = brief.Repository.Name
	}

	var indexedSha, primaryRef, indexedAt string
	var secondary []string
	for _, ref := range brief.Repository.Refs {
		if ref.Role == "primary" && primaryRef == "" {
			primaryRef, indexedSha, indexedAt = ref.Name, shortSha(ref.IndexedCommitSha), dateOnly(ref.IndexedAt)
			continue
		}
		if ref.Name != "" {
			secondary = append(secondary, ref.Name)
		}
	}

	header := "=== SynapCTX brief — " + name
	if localSha != "" {
		header += " · local HEAD " + localSha
		if localBranch != "" {
			header += " (" + localBranch + ")"
		}
	}
	if indexedSha != "" {
		header += " · indexed " + indexedSha
		switch {
		case primaryRef != "" && indexedAt != "":
			header += " (" + primaryRef + ", " + indexedAt + ")"
		case primaryRef != "":
			header += " (" + primaryRef + ")"
		}
	}
	if len(secondary) > 0 {
		header += " · also tracked: " + strings.Join(secondary, ", ")
	}
	header += " ==="

	var b strings.Builder
	b.WriteString(header + "\n")
	// One concrete call, printed verbatim, right under the status line it
	// belongs to — so a session that opens with recall_memory still sees the
	// retrieval it is skipping instead of a bare name in a deferred catalog.
	if brief.RetrievalHint != "" {
		fmt.Fprintf(&b, "Try first: %s\n", brief.RetrievalHint)
	}
	if brief.BootstrapNote != "" {
		fmt.Fprintf(&b, "%s\n", brief.BootstrapNote)
	}
	// The tool line names the namespace because that is the one fact no shipped
	// document can carry: it depends on what this developer called the server.
	fmt.Fprintf(&b, "Tools for this organization: %s (whole-org code graph, every repository at once), %s, find_references, get_dependents, get_service_dependencies, store_memory. They may be listed as deferred names without schemas — deferred is not unavailable; search for the tool and call it.\n",
		toolName(server, "retrieve_context", guessed), toolName(server, "recall_memory", guessed))
	b.WriteString("Open the first task with recall_memory(<the task>) and retrieve_context(<the task>), once each, before the first local search. Familiarity is not a reason to skip.\n")

	// The freshness line only exists when it can be TRUE. Saying "retrieval
	// describes the code in front of you" without knowing both shas would be a
	// claim we cannot support, and the failure mode is an agent trusting a stale
	// answer about code it just rewrote.
	if localSha != "" && indexedSha != "" {
		if localSha == indexedSha {
			b.WriteString("Local HEAD equals the indexed sha: retrieval describes the code in front of you.\n")
		} else {
			fmt.Fprintf(&b, "Local HEAD differs from the indexed sha: retrieval describes %s; run `sctx watch` to make uncommitted edits visible.\n", indexedSha)
		}
	}

	notes := brief.Notes
	if len(notes) > sessionNoteMax {
		notes = notes[:sessionNoteMax]
	}
	if len(notes) == 0 {
		// An empty result is still worth a line: silence would read as "SynapCTX
		// has nothing to offer here", when what it means is "you are the first".
		b.WriteString("\nNo org memory is bound to this repository yet — what you learn here is worth a store_memory.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nOrg memory about this repository (%d notes, newest first):\n", len(notes))
	for _, n := range notes {
		label := n.Kind
		if d := dateOnly(n.CreatedAt); d != "" {
			if label != "" {
				label += " · " + d
			} else {
				label = d
			}
		}
		text := truncateRunes(n.Text, sessionNoteMaxChars)
		if label != "" {
			fmt.Fprintf(&b, "• [%s] %s\n", label, text)
			continue
		}
		fmt.Fprintf(&b, "• %s\n", text)
	}
	b.WriteString("Record what you decide here with store_memory; supersede rather than forget.\n")
	return b.String()
}

// Proactive guidance v2, workspace brief (plan item B5): the childRepos scan
// bounds — cap 200 directory entries inspected, cap 100ms wall clock — and
// the final render bound (1,200 tokens) are all from the shared wire doc.
const (
	workspaceScanMaxEntries = 200
	workspaceScanWallBudget = 100 * time.Millisecond
	workspaceRepoCap        = 8
	workspaceBriefMaxTokens = 1200
	// bytesPerToken mirrors the CLI's own conservative token estimate
	// (CLAUDE.md: "Token estimate is bytes/4"), so the 1,200-token bound
	// from the wire doc is enforced the same way everywhere else in sctx
	// counts tokens.
	bytesPerToken = 4
)

// surfaceWorkspacePath is the new org-scoped brief: no single repository, so
// no per-repository memory bound to lean on — it exists for exactly the
// workspace-root case runClaudeWorkspaceBrief handles.
const surfaceWorkspacePath = "/v1/surface/for-workspace"

type forWorkspaceRequest struct {
	RepositoryNames []string `json:"repositoryNames"`
	SessionID       string   `json:"sessionId"`
	CWD             string   `json:"cwd"`
}

// forWorkspaceResponse mirrors forRepoResponse's own rule: every field is
// optional, because a partially populated brief is still worth printing.
type forWorkspaceResponse struct {
	Organization string `json:"organization"`
	Repositories []struct {
		Name             string `json:"name"`
		IndexingStatus   string `json:"indexingStatus"`
		PrimaryRef       string `json:"primaryRef"`
		IndexedAt        string `json:"indexedAt"`
		IndexedCommitSha string `json:"indexedCommitSha"`
	} `json:"repositories"`
	Notes []struct {
		ID           string   `json:"id"`
		Kind         string   `json:"kind"`
		CreatedAt    string   `json:"createdAt"`
		Text         string   `json:"text"`
		Repositories []string `json:"repositories"`
	} `json:"notes"`
	RetrievalHint string `json:"retrievalHint"`
	Tools         string `json:"tools"`
}

// runClaudeWorkspaceBrief implements the cwd-is-not-a-repository half of
// RunClaudeSessionStart: it lists the immediate child checkouts
// (gitrepo.ChildRepos), ranks them by how recently their `.git/index` was
// touched, keeps the busiest workspaceRepoCap, groups THOSE by organization
// (an origin URL's own org, via orgOf/normalizeURL) and asks for a brief
// scoped to whichever organization holds the most of them — the one this
// developer is most likely actually working across right now. Every failure
// is silence, including a 404 from an older proxy that predates this
// endpoint.
func runClaudeWorkspaceBrief(out io.Writer, cfg config.Config, call sessionStartCall, version string) int {
	if call.Source == "compact" {
		// Nothing repository-specific was said at startup for a workspace
		// root, so there is nothing worth repeating on a compaction either.
		return 0
	}

	children := gitrepo.ChildRepos(call.CWD, workspaceScanMaxEntries, workspaceScanWallBudget)
	if len(children) == 0 {
		return 0
	}
	if len(children) > workspaceRepoCap {
		children = children[:workspaceRepoCap]
	}

	byOrg := make(map[string][]string)
	for _, c := range children {
		org := orgOf(c.Name)
		if org == "" {
			continue
		}
		byOrg[org] = append(byOrg[org], c.Name)
	}
	var bestOrg string
	for org, names := range byOrg {
		if len(names) > len(byOrg[bestOrg]) {
			bestOrg = org
		}
	}
	if bestOrg == "" {
		return 0
	}

	token, _ := cfg.TokenForOrg(bestOrg)
	if token == "" {
		return 0
	}

	resp, err := fetchWorkspaceBrief(cfg, token, byOrg[bestOrg], call, version)
	if err != nil {
		return 0
	}

	home, _ := os.UserHomeDir()
	// No single repository root exists here, so claudeServerNameFor is asked
	// with an empty root — it still finds a USER-scope MCP entry, just never
	// a project-scoped `.mcp.json`.
	server := claudeServerNameFor(home, call.CWD, "", token)
	guessed := server == ""
	if guessed {
		server = bestOrg
	}
	if text := renderWorkspaceBrief(resp, server, guessed); text != "" {
		fmt.Fprint(out, text)
	}
	return 0
}

func fetchWorkspaceBrief(cfg config.Config, token string, names []string, call sessionStartCall, version string) (forWorkspaceResponse, error) {
	var out forWorkspaceResponse
	body, err := json.Marshal(forWorkspaceRequest{RepositoryNames: names, SessionID: call.SessionID, CWD: call.CWD})
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionStartBudget)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		surfaceEndpointFor(cfg.TelemetryEndpoint, surfaceWorkspacePath), bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", httpclient.UserAgent(version, "claude-code"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	// A 404 is an older proxy that predates this endpoint: silence, exactly
	// like any other failure here — never a reason to print an error to the
	// agent.
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("surface: %s", resp.Status)
	}
	if err := json.UnmarshalDecode(jsontext.NewDecoder(io.LimitReader(resp.Body, 256<<10)), &out); err != nil {
		return out, err
	}
	return out, nil
}

// renderWorkspaceBrief builds the plain-text workspace brief, bounded to
// workspaceBriefMaxTokens per the wire doc: header, one line per repository
// (name · status · indexed age), notes, hint, tools.
func renderWorkspaceBrief(resp forWorkspaceResponse, server string, guessed bool) string {
	if resp.Organization == "" && len(resp.Repositories) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "=== SynapCTX workspace brief — org %s ===\n", resp.Organization)
	for _, r := range resp.Repositories {
		age := dateOnly(r.IndexedAt)
		if age == "" {
			age = "not yet indexed"
		}
		fmt.Fprintf(&b, "• %s · %s · indexed %s\n", r.Name, r.IndexingStatus, age)
	}

	notes := resp.Notes
	if len(notes) > 4 {
		notes = notes[:4]
	}
	for _, n := range notes {
		label := n.Kind
		if d := dateOnly(n.CreatedAt); d != "" {
			if label != "" {
				label += " · " + d
			} else {
				label = d
			}
		}
		text := truncateRunes(n.Text, 280)
		if label != "" {
			fmt.Fprintf(&b, "• [%s] %s\n", label, text)
		} else {
			fmt.Fprintf(&b, "• %s\n", text)
		}
	}

	if resp.RetrievalHint != "" {
		fmt.Fprintf(&b, "Try first: %s\n", resp.RetrievalHint)
	}
	if resp.Tools != "" {
		fmt.Fprintf(&b, "%s\n", resp.Tools)
	} else {
		fmt.Fprintf(&b, "Tools for org %s: %s, %s, find_references, get_dependents, get_service_dependencies, store_memory.\n",
			resp.Organization, toolName(server, "retrieve_context", guessed), toolName(server, "recall_memory", guessed))
	}

	return truncateRunes(b.String(), workspaceBriefMaxTokens*bytesPerToken)
}

// shortSha is the 10 characters a human compares at a glance. Anything shorter
// collides across a large monorepo history; anything longer is unread.
func shortSha(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// dateOnly reduces an RFC3339 timestamp to a day. The hour a memory was written
// has never once changed a decision, and it costs a third of the line.
func dateOnly(ts string) string {
	if ts == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02")
	}
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ""
}

// truncateRunes cuts on a rune boundary: byte-slicing a note would emit a
// mangled character, and the note text is arbitrary developer prose.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max]), " \t\n") + "…"
}

// localHead reads the checked-out commit WITHOUT shelling out to git, for the
// same reason gitrepo does not: this runs in a session's startup path, and
// forking a process there costs more than everything else in this hook
// combined. Every failure returns empty and the caller drops the line.
func localHead(root string) (sha, branch string) {
	if root == "" {
		return "", ""
	}
	// Resolved rather than assumed to be `<root>/.git`: in a linked worktree or
	// a submodule that path is a FILE, and hard-coding it returned nothing —
	// dropping the freshness line on exactly the checkout least likely to match
	// the indexed ref. HEAD is per-worktree; refs are shared (see GitDirs).
	gitDir, commonDir, ok := gitrepo.GitDirs(root)
	if !ok {
		return "", ""
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", ""
	}
	head := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(head, "ref: ") {
		// Detached HEAD: the file holds the sha itself and there is no branch.
		return shortSha(head), ""
	}
	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
	branch = strings.TrimPrefix(ref, "refs/heads/")

	// Loose ref first — it is the common case and the authoritative one when
	// both exist, because `git pack-refs` leaves the loose file in place until
	// the next update.
	if loose, err := os.ReadFile(filepath.Join(commonDir, filepath.FromSlash(ref))); err == nil {
		if s := strings.TrimSpace(string(loose)); s != "" {
			return shortSha(s), branch
		}
	}
	if s := packedRef(filepath.Join(commonDir, "packed-refs"), ref); s != "" {
		return shortSha(s), branch
	}
	return "", branch
}

// packedRef scans `packed-refs` for one ref. Lines are "<sha> <ref>"; comments
// start with '#' and peeled tags with '^', both of which are skipped.
func packedRef(path, ref string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		sha, name, ok := strings.Cut(line, " ")
		if ok && strings.TrimSpace(name) == ref {
			return sha
		}
	}
	return ""
}
