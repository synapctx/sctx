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
const postToolBudget = 1200 * time.Millisecond

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
}

type forFileResponse struct {
	Notes []struct {
		Text      string `json:"text"`
		CreatedAt string `json:"createdAt"`
		ID        string `json:"id"`
	} `json:"notes"`
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
// to an agent mid-edit.
func RunClaudePostTool(in io.Reader, out io.Writer, cfg config.Config) int {
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
	var repo, subject, kind string
	switch call.ToolName {
	case "Edit", "Write", "NotebookEdit", "MultiEdit":
		absPath, _ := call.ToolInput["file_path"].(string)
		if absPath == "" {
			return 0
		}
		var rel string
		repo, rel = repoAndRelativePath(absPath, call.CWD)
		if rel == "" {
			return 0
		}
		subject, kind = rel, kindFile
	case "Bash":
		// Roadmap item 0b. Every rejection here happens BEFORE any network call,
		// because the cheapest nudge is the one we decide not to make — and this
		// fires on every Bash command the agent runs.
		cmd, _ := call.ToolInput["command"].(string)
		sym := grepSymbol(cmd)
		if sym == "" {
			return 0
		}
		repo = gitrepo.Detect(call.CWD)
		if repo == "" {
			// Without knowing where they searched there is no difference to
			// report, only a total — which is the noise this exists to avoid.
			return 0
		}
		subject, kind = sym, kindSymbol
	default:
		return 0
	}
	// No key means no organization to ask, and there is nothing useful to say
	// about that here — `sctx setup` and `sctx gain` own that conversation.
	token, _ := cfg.TokenForOrg(orgOf(repo))
	if token == "" {
		return 0
	}

	var body string
	switch kind {
	case kindFile:
		body = memoryContext(subject, fetchNotes(cfg, token, repo, subject))
	case kindSymbol:
		body = symbolContext(subject, fetchElsewhere(cfg, token, repo, subject))
	}
	if body == "" {
		return 0
	}
	writeAdditionalContext(out, "PostToolUse", body)
	return 0
}

const (
	kindFile   = "file"
	kindSymbol = "symbol"
)

// repoAndRelativePath turns the absolute path the hook was given into the
// (org/repo, repo-relative path) pair the server indexes by.
//
// Relative to the REPOSITORY ROOT, not to the agent's working directory: an
// agent's cwd wanders during a session, and a path resolved against it would
// silently stop matching anything the moment it changed.
func repoAndRelativePath(absPath, cwd string) (repo, rel string) {
	dir := filepath.Dir(absPath)
	root, name, ok := gitrepo.RootAndName(dir)
	if !ok {
		// Fall back to the agent's cwd only to name the repository; without a
		// root there is no relative path to compute, so we stop.
		_ = cwd
		return "", ""
	}
	r, err := filepath.Rel(root, absPath)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", ""
	}
	return name, filepath.ToSlash(r)
}

func orgOf(repoFullName string) string {
	if i := strings.IndexByte(repoFullName, '/'); i > 0 {
		return repoFullName[:i]
	}
	return ""
}

func fetchNotes(cfg config.Config, token, repo, rel string) []string {
	var out forFileResponse
	if err := postSurface(cfg, token, surfacePath,
		map[string]string{"repositoryName": repo, "filePath": rel}, &out); err != nil {
		return nil
	}
	notes := make([]string, 0, len(out.Notes))
	for _, n := range out.Notes {
		if n.CreatedAt != "" {
			notes = append(notes, fmt.Sprintf("(%s) %s", n.CreatedAt, n.Text))
			continue
		}
		notes = append(notes, n.Text)
	}
	return notes
}

type elsewhereResult struct {
	Elsewhere    int      `json:"elsewhere"`
	Repositories []string `json:"repositories"`
	Ambiguous    int      `json:"ambiguous"`
}

// fetchElsewhere asks for the call sites the developer's grep could not see.
func fetchElsewhere(cfg config.Config, token, repo, symbol string) elsewhereResult {
	var out elsewhereResult
	_ = postSurface(cfg, token, surfaceSymbolPath,
		map[string]string{"repositoryName": repo, "symbol": symbol}, &out)
	return out
}

// postSurface is the one HTTP shape both lookups share. Every failure is
// silence: sctx reads any error as "no suggestion", and an error rendered into
// an agent's context would be pure noise.
func postSurface(cfg config.Config, token, path string, payload map[string]string, into any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), postToolBudget)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, surfaceEndpointFor(cfg.TelemetryEndpoint, path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

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
// memoryContext renders what the organization knows about a file.
func memoryContext(rel string, notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SynapCTX organizational memory about %s — recorded by a teammate's agent, surfaced because you edited this file (nobody asked for it):\n", rel)
	for _, n := range notes {
		b.WriteString("\n• " + n + "\n")
	}
	b.WriteString("\nUse recall_memory for the full text, or store_memory if you learn something here that the next person will need.")
	return b.String()
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
