// Package agentdoc holds the instruction documents `sctx setup` installs, the
// table of coding agents it knows how to teach, and the pure text rendering that
// turns the two into the block written to an agent's instruction file.
//
// WHY THIS IS PUBLIC, WHEN THE REST OF THE INSTALL IS NOT. synapctx.com/sctx/
// publishes the same setup guidance for people who have not installed the binary
// yet, or whose agent is not in the table below. The alternative was a second copy
// of these documents living in the website's templates — and the drift that copy
// would accumulate is invisible in the worst possible way, because the public page
// is the one strangers read while the CLI is what customers actually run. One
// definition, imported by both, is the only arrangement where the two cannot
// disagree.
//
// The cut is DEFINITIONS AND PURE TEXT here, EVERYTHING THAT TOUCHES A DISK in
// internal/platform/agentsetup. Detection, sidecar writes and the refusal to
// overwrite an edited file all depend on seeing the machine, which a web page
// cannot do and must not pretend to.
//
// This is not a general-purpose API. It is exported for one consumer inside this
// organization and tracks the CLI's own release cycle; treat a signature here as
// stable only to the extent the `sctx setup` output is.
package agentdoc

import (
	"path"
	"strings"
)

// Block delimiters. Everything between them is ours and is replaced wholesale on
// reinstall; everything outside is the developer's and is never touched.
//
// A delimited block rather than a bare append is what makes this idempotent
// across releases: templates change, and without markers the only options are
// appending a second stale copy or rewriting a file we do not own.
//
// THE WEBSITE MUST WRAP ITS OUTPUT IN THESE TOO. Guidance that tells someone to
// paste bare text into CLAUDE.md leaves a file `sctx setup` cannot recognise as
// installed, so the next `--install` appends a second copy of the same document —
// which then loads twice into every session, forever. Publishing the markers is
// what makes the manual path and the automatic one converge instead of compound.
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

// BlockBody renders what belongs between the markers for one agent: include
// lines when the agent resolves them, the documents inlined when it does not.
//
// existing is the instruction file as it stands. A document the developer
// ALREADY includes is dropped: an include may be written by path
// (`@~/.claude/SCTX.md`) and adding our bare `@SCTX.md` beside it loads the
// same file twice, into every session, forever. That is what the first
// real-machine install did — the temporary-directory tests only ever produced
// the bare form, so the path form was never exercised.
//
// Pass an empty existing for the fresh-file case, which is what a web page
// showing "here is what to add" is describing.
func BlockBody(a Agent, orgs []string, docs []Doc, existing string) string {
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

// Wrap puts the markers around a rendered block, with exactly one trailing
// newline. Shared with the installer so the bytes a reader is told to paste are
// the bytes `sctx setup` would have written.
func Wrap(block string) string {
	return BeginMarker + "\n" + strings.TrimRight(block, "\n") + "\n" + EndMarker + "\n"
}

// BlockOf returns the text between our markers.
func BlockOf(s string) (string, bool) {
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

// WithoutBlock returns the file with our delimited block removed.
func WithoutBlock(s string) string {
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
	for _, line := range strings.Split(WithoutBlock(existing), "\n") {
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
