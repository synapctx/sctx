package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/synapctx/sctx/internal/platform/agentsetup"
	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/pkg/agentdoc"
)

const setupUsage = `usage: sctx setup [--install] [--force] [--agent <id>] [--list-agents]`

// runSetup reports — and with --install repairs — the agent-side half of the
// install: whether the coding agents configured on this machine have been told
// that sctx and the SynapCTX tools exist.
//
// This is a first-class command rather than a README step because the failure it
// catches is silent. `sctx` compresses output whether or not an agent knows what
// it is, and the MCP tools answer whether or not an agent knows they exist, so a
// half-finished install looks exactly like a finished one from every angle except
// the usage ledger. We have the measurement: with the instructions in place,
// retrieval ran at near parity with grep; without them, invocation fell 7.6x per
// unit of work.
func runSetup(cfg config.Config, args []string) int {
	install := false
	force := false
	only := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--install":
			install = true
		case "--force":
			// Overwrites SCTX.md / SYNAPCTX.md even when they already exist.
			// Normally they are never rewritten: an edited file was customised
			// on purpose and silently replacing it is worse than leaving it
			// stale. This is the explicit escape hatch for taking a hand-written
			// file back onto the shipped template.
			force = true
			install = true
		case "--list-agents":
			for _, a := range agentdoc.KnownAgents {
				fmt.Printf("  %-10s %-18s %s\n", a.ID, a.Name, a.Root)
			}
			return 0
		case "--agent":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "sctx: setup: --agent requires a value")
				return 2
			}
			only = args[i]
			if _, ok := agentdoc.AgentByID(only); !ok {
				fmt.Fprintf(os.Stderr, "sctx: setup: unknown agent %q (see --list-agents)\n", only)
				return 2
			}
		default:
			fmt.Fprintf(os.Stderr, "sctx: setup: unknown flag %q\n", args[i])
			fmt.Fprintln(os.Stderr, setupUsage)
			return 2
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: setup: %v\n", err)
		return 1
	}
	// Asked here because setup is the one command a human runs deliberately, on a
	// terminal, while thinking about what sctx is. Never on the wrapped path and
	// never in the hook: no terminal, no human, and a blocking prompt there would
	// stall every command the agent runs.
	if shouldAskConsent(cfg, term.IsTerminal(int(os.Stdin.Fd()))) {
		defer askConsent(cfg, os.Stdin, os.Stdout)
	}
	// --agent is an override for an agent we did not detect (a custom install
	// location, or a tool whose config lives somewhere we do not look). It is
	// the ONLY way a file is written where nothing was found, and it is explicit
	// by construction.
	if only != "" {
		if a, ok := agentdoc.AgentByID(only); ok {
			if err := os.MkdirAll(home+"/"+strings.SplitN(a.Detect[0], "/", 2)[0], 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "sctx: setup: %v\n", err)
				return 1
			}
		}
	}

	binary, err := os.Executable()
	if err != nil || binary == "" {
		binary = "sctx"
	}

	orgs := orgSlugs(cfg)
	orgTokens := codexOrgTokens(cfg)
	changedAny := false
	if install {
		installFn := agentsetup.InstallVersion
		if force {
			installFn = agentsetup.InstallForceVersion
		}
		changed, err := installFn(home, orgs, version, docsFor(cfg)...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sctx: setup: %v\n", err)
			return 1
		}
		for _, c := range changed {
			fmt.Println(c)
		}
		changedAny = len(changed) > 0

		if len(orgTokens) > 0 {
			// Every agent whose registry we manage, not only Codex. Before
			// 2026-08-18 this was Codex alone, so Kilo Code and OpenCode were
			// handed a document about tools that were never registered anywhere.
			mcpChanges, mcpErrs := agentsetup.InstallMCP(home, orgTokens, cfg.WorkspaceProxyURL, docsFor(cfg)...)
			for _, mcpErr := range mcpErrs {
				fmt.Fprintf(os.Stderr, "sctx: setup: MCP servers not installed: %v\n", mcpErr)
			}
			for _, c := range mcpChanges {
				fmt.Println(c)
			}
			changedAny = changedAny || len(mcpChanges) > 0
		}
		// Auto-wrap for every client that offers an interception point: a hook
		// process (Claude Code, Gemini CLI) or an in-process plugin (Kilo Code,
		// OpenCode). Without it the instructions still work — the agent types
		// `sctx` itself — but nothing is automatic.
		wrapChanges, wrapErrs := agentsetup.InstallWrapping(home, binary, docsFor(cfg)...)
		for _, wrapErr := range wrapErrs {
			fmt.Fprintf(os.Stderr, "sctx: setup: auto-wrap not installed: %v\n", wrapErr)
		}
		for _, c := range wrapChanges {
			fmt.Println(c)
		}
		changedAny = changedAny || len(wrapChanges) > 0
		if !changedAny {
			fmt.Println("already set up; nothing to change")
		} else {
			fmt.Println("\nRestart your agent — instructions, hooks and MCP servers are read at startup.")
		}
	}

	st, err := agentsetup.InspectWithCodexMCP(home, orgTokens, cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: setup: %v\n", err)
		return 1
	}
	printSetupStatus(os.Stdout, st, cfg, install)
	endpointOK := printMCPEndpointStatus(os.Stdout, st, cfg)
	hooksOK := true
	if hasAgent(st, "claude") {
		hooksOK = printHookStatus(os.Stdout, home, binary)
	}
	if st.Complete() && hooksOK && endpointOK {
		return 0
	}
	if !install {
		fmt.Println("\nFix with: sctx setup --install")
	}
	return 1
}

// probeResult is what the MCP host said when asked.
type probeResult int

const (
	// probeReachable: the host answered. With a credential to offer, it also
	// ACCEPTED it — the strong form.
	probeReachable probeResult = iota
	// probeRejected: the host is up and refused the key. A different failure
	// with a different fix, and the two must never print the same line.
	probeRejected
	probeUnreachable
)

// probeMCPEndpoint reports what the MCP host every registration points at says
// when asked. Replaceable so tests never touch the network.
var probeMCPEndpoint = httpProbeMCPEndpoint

// httpProbeMCPEndpoint asks the endpoint whether it is there and, when a
// credential is available, whether that credential works.
//
// It used to send a bare GET and treat ANY response as healthy, including the
// 401 that answer always was — so setup printed "[ok] responding (401
// Unauthorized)", which reads to a customer as "my key is broken" while actually
// meaning "the probe brought no key". Both halves were wrong to leave: the line
// alarmed people about a working install, and it could not have detected a
// revoked key if there had been one.
//
// Now it speaks the protocol: an MCP `initialize` over POST with the same
// Authorization header the registrations carry. 200 means the tools really are
// callable — the strongest statement setup can make without pretending to be an
// agent. 401/403 means the host is fine and the key is not, which is the one
// case that needs a human. Any other status still counts as reachable: this is
// not a conformance test, and a server that answers is a server that is there.
func httpProbeMCPEndpoint(endpoint, token string) (probeResult, string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return probeUnreachable, "no MCP host configured"
	}
	client := &http.Client{Timeout: 4 * time.Second}

	if token == "" {
		// Nothing to authenticate with yet. The only question left is whether
		// anything is listening, and any answer at all settles it.
		resp, err := client.Get(endpoint)
		if err != nil {
			return probeUnreachable, transportReason(err)
		}
		defer resp.Body.Close()
		return probeReachable, "responding (no API key configured yet — run `sctx init`)"
	}

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"sctx-setup","version":"1"}}}`)
	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return probeUnreachable, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	// Streamable HTTP servers may answer either way; asking for both keeps a
	// content-negotiation 406 from being mistaken for a rejected credential.
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return probeUnreachable, transportReason(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return probeReachable, "responding, and your API key was accepted"
	case http.StatusUnauthorized, http.StatusForbidden:
		return probeRejected, resp.Status
	default:
		return probeReachable, "responding (" + resp.Status + ")"
	}
}

// transportReason strips the URL out of a transport error. It is already on the
// line above, and repeating it turns a one-line diagnosis into three.
func transportReason(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// printMCPEndpointStatus says whether the host every registration points at is
// actually up, and whether it accepts this machine's credentials.
func printMCPEndpointStatus(w io.Writer, st agentsetup.Status, cfg config.Config) bool {
	managed := false
	for _, t := range st.Targets {
		if t.CodexMCP != nil || t.RemoteMCP != nil {
			managed = true
		}
	}
	if !managed {
		return true
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.WorkspaceProxyURL), "/")
	if !strings.HasSuffix(endpoint, "/mcp") {
		endpoint += "/mcp"
	}
	org, token := probeCredential(cfg)
	fmt.Fprintf(w, "\nMCP host every registration points at\n  %s\n", endpoint)

	result, detail := probeMCPEndpoint(endpoint, token)
	switch result {
	case probeReachable:
		if org != "" {
			fmt.Fprintf(w, "  [ok]      %s (checked with the %s key)\n", detail, org)
			return true
		}
		fmt.Fprintf(w, "  [ok]      %s\n", detail)
		return true
	case probeRejected:
		fmt.Fprintf(w, "  [rejected] the host is up and refused the %s key — %s\n", org, detail)
		fmt.Fprintln(w, "  the servers are registered, but every SynapCTX tool call will fail to authenticate.")
		fmt.Fprintf(w, "  re-authenticate that organization: sctx init --key <sctx_live_...>\n")
		return false
	default:
		fmt.Fprintf(w, "  [unreachable] %s\n", detail)
		fmt.Fprintln(w, "  the servers are registered but nothing is listening — every SynapCTX tool call will fail.")
		fmt.Fprintln(w, "  set workspace_proxy_url in ~/.config/sctx/config.toml, or re-run sctx init.")
		return false
	}
}

// probeCredential picks the key to test with: the default organization's, or
// the first configured. One key is enough — the question is whether this
// machine's credentials are accepted by that host, and a per-org check would
// send four requests to answer it four times.
func probeCredential(cfg config.Config) (org, token string) {
	tokens := codexOrgTokens(cfg)
	if len(tokens) == 0 {
		return "", ""
	}
	if t, ok := tokens[cfg.DefaultOrg]; ok && strings.TrimSpace(t) != "" {
		return cfg.DefaultOrg, t
	}
	slugs := make([]string, 0, len(tokens))
	for slug := range tokens {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs[0], tokens[slugs[0]]
}

// printWrappingStatus says, per agent, whether its commands are actually being
// wrapped — and by what.
//
// This is the half of setup that produces the savings, and it is the half that
// was invisible: setup reported instruction files and MCP servers, so a machine
// where nothing ever wrapped a command looked identical to one where everything
// did. The three mechanisms are named rather than abstracted away because the
// remedy differs — a hook lives in the client's settings, a plugin is a file,
// and a manual client has neither and never will.
func printWrappingStatus(w io.Writer, st agentsetup.Status, cfg config.Config) {
	binary, err := os.Executable()
	if err != nil || binary == "" {
		binary = "sctx"
	}
	states, err := agentsetup.InspectWrapping(st.Home, binary, docsFor(cfg)...)
	if err != nil || len(states) == 0 {
		return
	}
	fmt.Fprintln(w, "\ncommand wrapping (this is what produces the savings)")
	for _, ws := range states {
		label := "manual"
		switch ws.Mode {
		case agentdoc.WrapHook:
			label = "hook"
		case agentdoc.WrapPlugin:
			label = "plugin"
		}
		state := "[ok]     "
		switch {
		case ws.OK && ws.NeedsTrust:
			// Wired, and inert until a human acts. Never [ok]: this is the state
			// a customer must actually do something about.
			state = "[trust]  "
		case ws.Mode == agentdoc.WrapManual:
			// Not a failure: the client offers nothing to hook. Reported every
			// time anyway, because an agent that is not being wrapped has to be
			// typing `sctx` itself, and that is worth seeing.
			state = "[manual] "
		case !ws.OK:
			state = "[missing]"
		}
		fmt.Fprintf(w, "  %s %-16s %-7s %s\n", state, ws.AgentName, label, ws.Detail)
		if ws.Path != "" && (!ws.OK || ws.NeedsTrust) {
			fmt.Fprintf(w, "            %s\n", ws.Path)
		}
	}
}

// printHookStatus reports the hooks and returns whether they are all wired.
//
// Separate from the instruction-file status because they fail independently and
// look identical from outside: instructions tell an agent sctx EXISTS, hooks are
// what make it RUN. A machine can have either half and neither symptom.
func printHookStatus(w io.Writer, home, binary string) bool {
	path, states, err := agentsetup.InspectHooks(home, binary)
	if err != nil {
		fmt.Fprintf(w, "\nhooks (%s)\n  [error] %v\n", path, err)
		return false
	}
	fmt.Fprintf(w, "\nhooks (%s)\n", path)
	ok := true
	for _, st := range states {
		// The matcher is printed, not just the event: sctx now installs TWO
		// PreToolUse hooks (Bash and Grep|Glob|Agent), and a status listing that
		// names only the event shows one as installed and one as missing with no
		// way to tell which is which.
		label := fmt.Sprintf("%s(%s)", st.Event, st.Matcher)
		if st.Installed {
			fmt.Fprintf(w, "  [ok]      %-34s %s\n", label, st.Purpose)
			continue
		}
		ok = false
		fmt.Fprintf(w, "  [missing] %-34s %s\n", label, st.Purpose)
	}
	return ok
}

// docsFor returns the instruction documents this machine should have. SYNAPCTX.md
// is only offered once an API key exists: describing tools the agent cannot call
// produces failed calls and teaches it the whole file is unreliable.
func docsFor(cfg config.Config) []agentdoc.Doc {
	docs := []agentdoc.Doc{agentdoc.SctxDoc}
	if len(codexOrgTokens(cfg)) > 0 {
		docs = append(docs, agentdoc.SynapctxDoc)
	}
	return docs
}

func orgSlugs(cfg config.Config) []string {
	tokens := codexOrgTokens(cfg)
	slugs := make([]string, 0, len(tokens))
	for slug := range tokens {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

func printSetupStatus(w io.Writer, st agentsetup.Status, cfg config.Config, afterInstall bool) {
	fmt.Fprintln(w, "\ncoding agents on this machine")
	if !st.Detected() {
		// Never default to the most popular agent. Say what was looked for, so
		// "none found" is actionable rather than an accusation.
		fmt.Fprintln(w, "  none detected — nothing was created.")
		fmt.Fprintf(w, "  looked for: %s (under %s)\n", strings.Join(st.Searched, ", "), st.Home)
		fmt.Fprintln(w, "\n  If you use one of these, install it first, then re-run.")
		fmt.Fprintln(w, "  Using something else? sctx setup --list-agents, then --agent <id>.")
		return
	}
	for _, t := range st.Targets {
		switch {
		case t.InstructionsOK():
			fmt.Fprintf(w, "  [ok]      %-16s %s\n", t.Name, t.RootPath)
		case t.Stale:
			fmt.Fprintf(w, "  [stale]   %-16s instructions are from an older sctx — %s\n", t.Name, t.RootPath)
		case t.Installed:
			// Taught, but a document beside it is missing or out of date. Naming
			// the document matters: the instruction file itself is fine, so
			// "[missing] Claude Code" would send someone looking at the wrong file.
			fmt.Fprintf(w, "  [stale]   %-16s a document needs updating — %s\n", t.Name, filepath.Dir(t.RootPath))
		default:
			fmt.Fprintf(w, "  [missing] %-16s not told about SynapCTX — %s\n", t.Name, t.RootPath)
		}
		// Documents we may not touch. Reported rather than counted as incomplete:
		// neither has a remedy a plain --install can apply, so nagging would be a
		// warning with no action, which is how warnings get muted.
		for _, s := range t.Attention() {
			// The PATH, not just the name. A document loaded by the developer's own
			// include can live anywhere they filed it, and a remedy that names only
			// "SCTX.md" sends someone to the copy beside CLAUDE.md — which may not
			// be the file their agent actually reads.
			switch s.State {
			case agentdoc.SidecarEdited:
				fmt.Fprintf(w, "            %-13s edited here — left as is (--install --force to reset) — %s\n", s.Name, s.Path)
			case agentdoc.SidecarUnverifiable:
				fmt.Fprintf(w, "            %-13s cannot be verified as ours — run --install --force once — %s\n", s.Name, s.Path)
			}
		}
		// Documents that ARE managed but out of date. The target line above says an
		// agent needs updating; this says which document and, when its stamp
		// records one, which sctx wrote it.
		for _, s := range t.Sidecars {
			switch s.State {
			case agentdoc.SidecarStale:
				from := "an older sctx"
				if s.Version != "" {
					from = s.Version
				}
				fmt.Fprintf(w, "            %-13s from %s — updates on --install — %s\n", s.Name, from, s.Path)
			case agentdoc.SidecarMissing:
				fmt.Fprintf(w, "            %-13s not written yet — installs on --install — %s\n", s.Name, s.Path)
			}
		}
	}
	for _, t := range st.Targets {
		if t.CodexMCP == nil {
			continue
		}
		fmt.Fprintf(w, "\nCodex MCP servers (%s)\n", t.CodexMCP.ConfigPath)
		state := "ok"
		detail := "registered"
		switch {
		case len(t.CodexMCP.Conflicts) > 0:
			state, detail = "conflict", "unmanaged registration with this name already exists"
		case !t.CodexMCP.Installed:
			state, detail = "missing", "not registered"
		case t.CodexMCP.Stale:
			state, detail = "stale", "endpoint, organizations or credentials changed"
		}
		for _, server := range t.CodexMCP.Servers {
			fmt.Fprintf(w, "  [%-8s] %-24s %s\n", state, server, detail)
		}
	}
	for _, t := range st.Targets {
		if t.RemoteMCP == nil {
			continue
		}
		fmt.Fprintf(w, "\n%s MCP servers (%s)\n", t.Name, t.RemoteMCP.ConfigPath)
		if t.RemoteMCP.Unreadable {
			fmt.Fprintf(w, "  [error   ] %-24s not valid JSON — left unchanged\n", filepath.Base(t.RemoteMCP.ConfigPath))
			continue
		}
		conflicting := map[string]bool{}
		for _, c := range t.RemoteMCP.Conflicts {
			conflicting[strings.SplitN(c, " ", 2)[0]] = true
		}
		for _, server := range t.RemoteMCP.Servers {
			state, detail := "ok", "registered"
			switch {
			case conflicting[server]:
				state, detail = "conflict", "a registration with this name already exists elsewhere"
			case !t.RemoteMCP.Installed:
				state, detail = "missing", "not registered"
			case t.RemoteMCP.Stale:
				state, detail = "stale", "endpoint, organizations or credentials changed"
			}
			fmt.Fprintf(w, "  [%-8s] %-24s %s\n", state, server, detail)
		}
	}
	// Agents we teach but whose MCP registry we do not write. Said out loud
	// because the instruction document we just installed describes those tools:
	// silence here reads as "registered", and the customer would only find out
	// when the agent called a tool that does not exist.
	if len(orgSlugs(cfg)) > 0 {
		var unmanaged []string
		for _, t := range st.Targets {
			if t.CodexMCP == nil && t.RemoteMCP == nil {
				unmanaged = append(unmanaged, t.Name)
			}
		}
		if len(unmanaged) > 0 {
			fmt.Fprintf(w, "\nMCP servers sctx does not manage: %s\n", strings.Join(unmanaged, ", "))
			fmt.Fprintln(w, "  register the SynapCTX servers in that client yourself — sctx cannot verify they exist")
		}
	}
	printWrappingStatus(w, st, cfg)
	fmt.Fprintln(w, "\ninstruction documents")
	fmt.Fprintln(w, "  written as sidecar files where includes are supported; otherwise inlined into the agent's root instructions")
	for _, d := range st.Docs {
		fmt.Fprintf(w, "  %-13s %s\n", d.Name, d.Purpose)
	}
	if len(orgSlugs(cfg)) == 0 {
		fmt.Fprintf(w, "  %-13s needs an API key first: sctx init\n", agentdoc.SynapctxDoc.Name)
	}
}

// shouldNudge is the whole decision, extracted so the guarantee can be tested.
// Left inline it would be untestable-by-construction: a test process has no TTY,
// so "the nudge never reaches an agent" would pass without the check existing.
//
// The TTY condition is not a nicety. sctx's stdout and stderr are read by an
// AGENT on every wrapped command, and a setup notice there costs the customer
// tokens on a product sold on saving them — and invites the agent to act on it
// mid-task. stderr is a terminal exactly when a human is watching; the PreToolUse
// hook path never is.
//
// A machine with no agent detected is NOT nudged on the wrapped path. We would be
// telling someone to fix something we cannot see, on every command, forever.
// `sctx setup` and `sctx gain` say it when asked; that is enough.
func shouldNudge(st agentsetup.Status, stderrIsTerminal bool, suppress string) bool {
	if suppress != "" || !stderrIsTerminal {
		return false
	}
	return st.Detected() && !st.Complete()
}

func nudgeSetup(cfg config.Config) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	st, err := agentsetup.InspectWithCodexMCP(home, codexOrgTokens(cfg), cfg.WorkspaceProxyURL, docsFor(cfg)...)
	if err != nil {
		return
	}
	if !shouldNudge(st, term.IsTerminal(int(os.Stderr.Fd())), os.Getenv("SCT__NO_SETUP_NUDGE")) {
		return
	}
	stamp := cfg.SpoolDir + "/.setup-nudge"
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return
	}
	if err := os.MkdirAll(cfg.SpoolDir, 0o755); err != nil {
		return
	}
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr,
		"\nsctx: %s — savings and retrieval are both lower than they need to be.\n      Run: sctx setup --install\n",
		pendingLine(st))
}

// gainNotice is the line `sctx gain` prints under the savings summary.
//
// `gain` is where a developer goes to look at their savings, so it is the one
// moment they are already thinking about whether this tool is earning its place
// — and the only routine command whose output a human reads on purpose. Unlike
// the wrapped-path nudge it is not rate-limited and not conditional on
// detection: the user asked for a report, and "your agent has not been told any
// of this exists" is part of an honest one.
func gainNotice(st agentsetup.Status) string {
	if st.Complete() {
		return ""
	}
	if !st.Detected() {
		return "[warn] No coding agent detected — run `sctx setup` to see what was looked for"
	}
	return "[warn] " + pendingLine(st) + " — your savings are lower than they need to be. Run `sctx setup --install`"
}

func pendingLine(st agentsetup.Status) string {
	pending := st.Pending()
	for _, t := range pending {
		if t.CodexMCP != nil && !t.CodexMCP.Complete() && t.InstructionsOK() {
			return "OpenAI Codex has SynapCTX instructions but no usable MCP registration"
		}
	}
	names := make([]string, 0, len(pending))
	stale := true
	for _, t := range pending {
		names = append(names, t.Name)
		if !t.Stale {
			stale = false
		}
	}
	if stale && len(pending) > 0 {
		return joinAnd(names) + " has SynapCTX instructions from an older sctx"
	}
	return joinAnd(names) + " has not been told SynapCTX exists"
}

func hasAgent(st agentsetup.Status, id string) bool {
	for _, t := range st.Targets {
		if t.ID == id {
			return true
		}
	}
	return false
}

// codexOrgTokens resolves the credentials Codex must persist. Sectioned keys
// are the normal path and deliberately do not inherit the telemetry-only
// SCT__TELEMETRY_TOKEN override: one org-scoped key copied onto every server
// would make all but one fail. A legacy or environment key is usable only when
// its organization is known, because the server name is part of the tool
// namespace.
func codexOrgTokens(cfg config.Config) map[string]string {
	out := make(map[string]string, len(cfg.OrgTokens))
	for org, token := range cfg.OrgTokens {
		if strings.TrimSpace(org) != "" && strings.TrimSpace(token) != "" {
			out[org] = token
		}
	}
	if len(out) == 0 && cfg.DefaultOrg != "" {
		token := cfg.TelemetryTokenEnv
		if token == "" {
			token = cfg.LegacyToken
		}
		if token != "" {
			out[cfg.DefaultOrg] = token
		}
	}
	return out
}

func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		out := ""
		for i, n := range names {
			switch {
			case i == 0:
				out = n
			case i == len(names)-1:
				out += " and " + n
			default:
				out += ", " + n
			}
		}
		return out
	}
}
