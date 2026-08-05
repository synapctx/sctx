package main

import (
	"fmt"
	"io"
	"os"
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
	if install {
		installFn := agentsetup.Install
		if force {
			installFn = agentsetup.InstallForce
		}
		changed, err := installFn(home, orgs, docsFor(cfg)...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sctx: setup: %v\n", err)
			return 1
		}
		if len(changed) == 0 {
			fmt.Println("already set up; nothing to change")
		}
		for _, c := range changed {
			fmt.Println(c)
		}
		hookChanges, hookErr := agentsetup.InstallHooks(home, binary)
		if hookErr != nil {
			// A settings file we could not parse is one we must not write.
			fmt.Fprintf(os.Stderr, "sctx: setup: hooks not installed: %v\n", hookErr)
		}
		changed = append(changed, hookChanges...)
		if len(changed) > 0 {
			fmt.Println("\nRestart your agent — instruction files and hooks are read at startup.")
		}
	}

	st, err := agentsetup.Inspect(home, orgs, docsFor(cfg)...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: setup: %v\n", err)
		return 1
	}
	printSetupStatus(os.Stdout, st, cfg, install)
	hooksOK := printHookStatus(os.Stdout, home, binary)
	if st.Complete() && hooksOK {
		return 0
	}
	if !install {
		fmt.Println("\nFix with: sctx setup --install")
	}
	return 1
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
		if st.Installed {
			fmt.Fprintf(w, "  [ok]      %-12s %s\n", st.Event, st.Purpose)
			continue
		}
		ok = false
		fmt.Fprintf(w, "  [missing] %-12s %s\n", st.Event, st.Purpose)
	}
	return ok
}

// docsFor returns the instruction documents this machine should have. SYNAPCTX.md
// is only offered once an API key exists: describing tools the agent cannot call
// produces failed calls and teaches it the whole file is unreliable.
func docsFor(cfg config.Config) []agentdoc.Doc {
	docs := []agentdoc.Doc{agentdoc.SctxDoc}
	if len(orgSlugs(cfg)) > 0 {
		docs = append(docs, agentdoc.SynapctxDoc)
	}
	return docs
}

func orgSlugs(cfg config.Config) []string {
	slugs := make([]string, 0, len(cfg.OrgTokens))
	for slug := range cfg.OrgTokens {
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
		case t.OK():
			fmt.Fprintf(w, "  [ok]      %-16s %s\n", t.Name, t.RootPath)
		case t.Stale:
			fmt.Fprintf(w, "  [stale]   %-16s instructions are from an older sctx — %s\n", t.Name, t.RootPath)
		default:
			fmt.Fprintf(w, "  [missing] %-16s not told about SynapCTX — %s\n", t.Name, t.RootPath)
		}
	}
	fmt.Fprintln(w, "\nwhat gets installed")
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
	st, err := agentsetup.Inspect(home, orgSlugs(cfg), docsFor(cfg)...)
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
