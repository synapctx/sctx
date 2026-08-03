// Package agentsetup inspects and repairs the AGENT-SIDE half of a SynapCTX
// install: the instruction files that tell a coding agent that `sctx` and the
// SynapCTX MCP tools exist and when to reach for them.
//
// Why this is a product feature and not a README step. On 2026-07-31 we measured
// what happens when those instructions are absent: retrieval ran at near parity
// with `grep` while `~/.claude/CLAUDE.md` referenced SYNAPCTX.md, and invocation
// fell 7.6x per unit of work in the four days after the reference was removed.
// The tool was not being chosen on merit — it was being chosen because something
// told the agent it existed.
//
// That instruction lived in one developer's home directory, so it shipped with
// nothing. A customer who installs `sctx` and pastes an API key gets a working
// platform their agent never calls. This package is what closes that gap: the
// binary that IS reliably invoked is the one that can tell you the rest is not.
//
// It writes to whichever agents are ACTUALLY CONFIGURED on the machine — see
// agents.go. Assuming Claude Code would leave a Codex or Gemini user with a
// file nothing reads and an agent nothing told.
//
// Wording is deliberately TRIGGER-shaped ("when you are about to X, call Y")
// rather than mandate-shaped ("always call Y first"). Both were measured: the one
// tool that kept an imperative trigger was used while five rewritten as neutral
// capability descriptions were used zero times in the same session — and a blanket
// mandate destroys our ability to tell whether the tool was worth calling at all.
package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Block delimiters. Everything between them is ours and is replaced wholesale on
// reinstall; everything outside is the developer's and is never touched.
//
// A delimited block rather than a bare append is what makes this idempotent
// across releases: templates change, and without markers the only options are
// appending a second stale copy or rewriting a file we do not own.
const (
	BeginMarker = "<!-- BEGIN SYNAPCTX — managed by `sctx setup`; edits inside are replaced -->"
	EndMarker   = "<!-- END SYNAPCTX -->"
)

// Doc is one instruction document.
type Doc struct {
	Name    string // file name when written as a sidecar, e.g. "SCTX.md"
	Purpose string // one line, shown in status output
	Body    func(orgs []string) string
}

// SctxDoc and SynapctxDoc are the two halves. They are separate because they are
// earned separately: `sctx` works standalone with no account, while the MCP tools
// need an API key. Telling someone about tools they cannot call is how a setup
// guide loses its credibility.
var SctxDoc = Doc{
	Name:    "SCTX.md",
	Purpose: "tells the agent which commands sctx wraps, so it stops re-explaining the output",
	Body:    sctxBody,
}

var SynapctxDoc = Doc{
	Name:    "SYNAPCTX.md",
	Purpose: "tells the agent the org's code graph and memory are searchable, and when to search",
	Body:    synapctxBody,
}

// Target is one detected agent and what we found for it.
type Target struct {
	Agent
	RootPath  string // absolute
	Installed bool   // our block is present
	Stale     bool   // present, but its content is not what we would write now
}

// OK reports whether this agent is taught and current.
func (t Target) OK() bool { return t.Installed && !t.Stale }

// Status is the whole agent-side picture for one machine.
type Status struct {
	Home     string
	Targets  []Target // detected agents only
	Searched []string // every path consulted, for the nothing-found case
	Docs     []Doc    // what we would install
}

// Detected reports whether any known agent is configured here.
func (s Status) Detected() bool { return len(s.Targets) > 0 }

// Complete reports whether every detected agent is taught and current. A machine
// with NO agent is deliberately not "complete": there is nothing to be complete
// about, and reporting success would hide the thing the user needs to know.
func (s Status) Complete() bool {
	if !s.Detected() {
		return false
	}
	for _, t := range s.Targets {
		if !t.OK() {
			return false
		}
	}
	return true
}

// Pending returns the detected agents that are missing or out of date.
func (s Status) Pending() []Target {
	var out []Target
	for _, t := range s.Targets {
		if !t.OK() {
			out = append(out, t)
		}
	}
	return out
}

// Inspect reports the agent-side state under home for the given documents.
func Inspect(home string, orgs []string, docs ...Doc) (Status, error) {
	if home == "" {
		return Status{}, errors.New("no home directory")
	}
	if len(docs) == 0 {
		docs = []Doc{SctxDoc, SynapctxDoc}
	}
	st := Status{Home: home, Searched: DetectPaths(), Docs: docs}

	for _, a := range KnownAgents {
		if !configured(home, a) {
			continue
		}
		t := Target{Agent: a, RootPath: filepath.Join(home, a.Root)}
		body, err := os.ReadFile(t.RootPath)
		if err != nil && !os.IsNotExist(err) {
			return Status{}, fmt.Errorf("reading %s: %w", t.RootPath, err)
		}
		if got, ok := blockOf(string(body)); ok {
			t.Installed = true
			t.Stale = strings.TrimSpace(got) != strings.TrimSpace(blockBody(a, orgs, docs, string(body)))
		} else if strings.TrimSpace(blockBody(a, orgs, docs, string(body))) == "" {
			// Nothing left to add: the developer already includes every document
			// themselves. That agent IS taught, and saying otherwise would nag
			// forever about a machine that is correctly configured — while
			// installing an empty block to prove it.
			t.Installed = true
		}
		st.Targets = append(st.Targets, t)
	}
	return st, nil
}

// configured reports whether an agent has left its own configuration here. Only
// existence is checked — we never infer an agent from anything we wrote.
func configured(home string, a Agent) bool {
	for _, p := range a.Detect {
		if _, err := os.Stat(filepath.Join(home, p)); err == nil {
			return true
		}
	}
	return false
}

// blockBody renders what belongs between the markers for one agent: include
// lines when the agent resolves them, the documents inlined when it does not.
//
// existing is the instruction file as it stands. A document the developer
// ALREADY includes is dropped: an include may be written by path
// (`@~/.claude/SCTX.md`) and adding our bare `@SCTX.md` beside it loads the
// same file twice, into every session, forever. That is what the first
// real-machine install did — the temporary-directory tests only ever produced
// the bare form, so the path form was never exercised.
func blockBody(a Agent, orgs []string, docs []Doc, existing string) string {
	var b strings.Builder
	if a.Includes {
		for _, d := range docs {
			if referencesDoc(existing, d.Name) {
				continue
			}
			b.WriteString("@" + d.Name + "\n")
		}
		return b.String()
	}
	for i, d := range docs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(d.Body(orgs)))
		b.WriteString("\n")
	}
	return b.String()
}

// blockOf returns the text between our markers.
func blockOf(s string) (string, bool) {
	i := strings.Index(s, BeginMarker)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(BeginMarker):]
	j := strings.Index(rest, EndMarker)
	if j < 0 {
		// An opening marker with no close means a truncated or hand-edited file.
		// Treated as not installed so a reinstall appends a well-formed block,
		// rather than as a parse error the user cannot act on.
		return "", false
	}
	return rest[:j], true
}

// referencesDoc reports whether the file already loads a document by this name,
// in any form an include can take: `@SCTX.md`, `@~/.claude/SCTX.md`,
// `@./docs/SCTX.md`. Only the final path segment is compared, because that is
// what identifies the document; the prefix is the developer's filing choice.
//
// Our OWN block is excluded from the scan. Reading the include we wrote as if
// the developer had written it would drop it from the block on the next
// install, emptying the block and silently UNTEACHING the agent — the same
// shape as keying detection on a file we write.
func referencesDoc(existing, name string) bool {
	for _, line := range strings.Split(withoutBlock(existing), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "@") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "@"))
		if len(fields) == 0 {
			continue
		}
		// Includes are written with forward slashes on every platform, so path
		// (not filepath) is correct here even on Windows.
		if path.Base(fields[0]) == name {
			return true
		}
	}
	return false
}

// withoutBlock returns the file with our delimited block removed.
func withoutBlock(s string) string {
	i := strings.Index(s, BeginMarker)
	if i < 0 {
		return s
	}
	rest := s[i+len(BeginMarker):]
	j := strings.Index(rest, EndMarker)
	if j < 0 {
		return s[:i]
	}
	return s[:i] + rest[j+len(EndMarker):]
}

// Install teaches every detected agent, and reports what it changed so a caller
// can print the truth rather than a fixed success message.
//
// Sidecar documents (include-capable agents only) are never rewritten if they
// already exist: a developer who edited SCTX.md customised it on purpose, and
// silently replacing that is worse than leaving it slightly stale. The BLOCK is
// always brought current — it is explicitly marked as ours.
// Install writes the shipped documents for every detected agent.
//
// Existing sidecar documents are never rewritten — see the doc comment above.
// InstallForce is the explicit opt-out.
func Install(home string, orgs []string, docs ...Doc) ([]string, error) {
	return install(home, orgs, false, docs...)
}

// InstallForce rewrites sidecar documents that already exist, taking a
// hand-edited file back onto the shipped template. Only ever called for an
// explicit `--force`: the whole reason Install refuses is that a customised file
// was customised on purpose.
func InstallForce(home string, orgs []string, docs ...Doc) ([]string, error) {
	return install(home, orgs, true, docs...)
}

func install(home string, orgs []string, force bool, docs ...Doc) ([]string, error) {
	st, err := Inspect(home, orgs, docs...)
	if err != nil {
		return nil, err
	}
	if !st.Detected() {
		return nil, fmt.Errorf("no coding agent detected under %s (looked for %s)",
			home, strings.Join(st.Searched, ", "))
	}

	var changed []string
	for _, t := range st.Targets {
		if t.OK() && !force {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(t.RootPath), 0o755); err != nil {
			return changed, fmt.Errorf("creating %s: %w", filepath.Dir(t.RootPath), err)
		}
		if t.Includes {
			for _, d := range st.Docs {
				side := filepath.Join(filepath.Dir(t.RootPath), d.Name)
				if _, err := os.Stat(side); err == nil && !force {
					continue
				}
				if err := os.WriteFile(side, []byte(d.Body(orgs)), 0o644); err != nil {
					return changed, fmt.Errorf("writing %s: %w", side, err)
				}
				changed = append(changed, "wrote "+side)
			}
		}
		body, err := os.ReadFile(t.RootPath)
		if err != nil && !os.IsNotExist(err) {
			return changed, fmt.Errorf("reading %s: %w", t.RootPath, err)
		}
		block := blockBody(t.Agent, orgs, st.Docs, string(body))
		// Nothing to add — the developer already includes every document
		// themselves. Write no block, and remove one we left earlier: an empty
		// managed block is pure noise in a file we do not own, and on a reinstall
		// it reads as though the install produced nothing.
		out := upsertBlock(string(body), block)
		if strings.TrimSpace(block) == "" {
			out = strings.TrimRight(withoutBlock(string(body)), "\n") + "\n"
		}
		if err := os.WriteFile(t.RootPath, []byte(out), 0o644); err != nil {
			return changed, fmt.Errorf("writing %s: %w", t.RootPath, err)
		}
		verb := "taught"
		if t.Stale {
			verb = "updated"
		}
		changed = append(changed, fmt.Sprintf("%s %s (%s)", verb, t.Name, t.RootPath))
	}
	return changed, nil
}

// upsertBlock replaces our delimited block, or appends one.
//
// Appended, never prepended: the instruction file is the developer's and its
// opening lines are the ones they wrote. Trailing newlines are normalised to
// exactly one blank line, so the block can neither land on the end of an
// existing sentence — which rewrites their last instruction AND installs
// nothing — nor accumulate blank lines across repeated runs.
func upsertBlock(existing, block string) string {
	wrapped := BeginMarker + "\n" + strings.TrimRight(block, "\n") + "\n" + EndMarker + "\n"
	if i := strings.Index(existing, BeginMarker); i >= 0 {
		rest := existing[i+len(BeginMarker):]
		if j := strings.Index(rest, EndMarker); j >= 0 {
			tail := rest[j+len(EndMarker):]
			return existing[:i] + strings.TrimSuffix(wrapped, "\n") + tail
		}
	}
	out := strings.TrimRight(existing, "\n")
	if out != "" {
		out += "\n\n"
	}
	return out + wrapped
}
