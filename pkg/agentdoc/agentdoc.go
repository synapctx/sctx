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
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

// Sidecar provenance.
//
// THE PROBLEM THIS SOLVES: a template fix used to reach no machine that had
// already installed. `Install` refuses to overwrite an existing sidecar — right,
// because a developer may have customised it — but it could not tell a CUSTOMISED
// file from an untouched one carrying last release's text, so it treated both as
// sacred. `Inspect` never read sidecars at all, so `sctx setup` did not even
// report the drift. The result: correctness fixes (a tool that does not exist, a
// false claim about a subcommand) shipped to new machines only, silently, and
// adding a second org with `sctx init` never updated the scope section either.
//
// A hash of the body we wrote, written beside it, is what makes "ours and
// untouched" a decidable question. Untouched files then update on a plain
// `--install`; edited ones still refuse without `--force`.
//
// IN THE FILE rather than a local manifest, because synapctx.com serves these
// same bytes for people who install by hand. A manifest would make the manual
// path permanently unverifiable — the exact divergence the marker round-trip
// rules already exist to prevent.
//
// THE VERSION IS REPORTING, THE HASH IS THE DECISION. A stamp written by the CLI
// also records the sctx that wrote it, so `sctx setup` can say "from v0.4.2"
// instead of "from an older sctx" — but nothing branches on it. Versions can
// repeat (a `dev` build), regress (a downgrade) and be absent (the website
// renders these documents without one), while the hash answers "is this ours and
// untouched" in every one of those cases. A stamp carrying only a hash therefore
// remains fully valid, which is what keeps files installed before this change —
// and the ones the website serves today — updating on a plain `--install`.
const (
	stampPrefix = "<!-- sctx-doc "
	stampSuffix = " -->"
)

// StampInfo is the provenance recorded in a document's first line. Version is
// empty for a stamp written without one; that is expected, not damage.
type StampInfo struct {
	Version string
	Hash    string
}

// SidecarState classifies an existing sidecar document. It is what decides
// whether a fix may be delivered without asking.
type SidecarState int

const (
	// SidecarMissing — no file. Write it.
	SidecarMissing SidecarState = iota
	// SidecarCurrent — ours, untouched, and already what we would write.
	SidecarCurrent
	// SidecarStale — ours, untouched, but the template has moved on. Safe to
	// overwrite on a plain install: nothing of the developer's is in it.
	SidecarStale
	// SidecarEdited — stamped, but the body no longer hashes to its stamp. The
	// developer changed it on purpose; refuse without --force.
	SidecarEdited
	// SidecarUnverifiable — no stamp, so it predates stamping (or was pasted
	// from a pre-stamp page). We cannot prove it is untouched, so we do not
	// touch it. Reported, with the one-time remedy.
	SidecarUnverifiable
)

func (s SidecarState) String() string {
	switch s {
	case SidecarMissing:
		return "missing"
	case SidecarCurrent:
		return "current"
	case SidecarStale:
		return "stale"
	case SidecarEdited:
		return "edited"
	case SidecarUnverifiable:
		return "unverifiable"
	}
	return "unknown"
}

// bodyHash is a short content hash. Truncated to 12 hex characters because this
// detects accidental divergence, not a forgery — and a full hash would cost every
// session tokens to say the same thing.
func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:12]
}

// Stamp returns the provenance line for a document body, with no version. It is
// what a renderer that does not know which sctx it corresponds to should write —
// synapctx.com today — and it stays valid forever.
func Stamp(body string) string { return StampFor("", body) }

// StampFor returns the provenance line recording both the writing sctx version
// and the body hash.
//
// A version that is not a single token — empty, whitespace, or something that
// would close the comment early — degrades to the version-less Stamp rather than
// being escaped or truncated. The stamp's job is to be read back byte-for-byte;
// writing one the parser has to guess at would trade a missing nicety for a
// document nothing can verify.
func StampFor(version, body string) string {
	fields := strings.Fields(version)
	if len(fields) != 1 || strings.Contains(fields[0], "-->") {
		return stampPrefix + bodyHash(body) + stampSuffix
	}
	return stampPrefix + fields[0] + " " + bodyHash(body) + stampSuffix
}

// StampedBody returns the exact bytes to WRITE for a sidecar: the stamp, then the
// document. Both the installer and the website must use this (or StampedBodyFor),
// or a hand-installed file is permanently unverifiable.
func StampedBody(body string) string { return StampedBodyFor("", body) }

// StampedBodyFor is StampedBody with the writing sctx version recorded.
func StampedBodyFor(version, body string) string { return StampFor(version, body) + "\n" + body }

// ParseStamp splits a stamped file into its recorded hash and the body beneath.
// ok is false when the first line is not one of our stamps, which means the file
// predates stamping — never that it is malformed.
func ParseStamp(file string) (hash, rest string, ok bool) {
	info, rest, ok := ParseStampInfo(file)
	return info.Hash, rest, ok
}

// ParseStampInfo splits a stamped file into its provenance and the body beneath.
//
// The HASH IS THE LAST FIELD, not the first, which is what lets a versioned stamp
// and a version-less one be read by the same parser: everything before it is
// provenance a future release may add to, and a reader that does not understand
// the extra fields still recovers the one value that decides anything.
func ParseStampInfo(file string) (StampInfo, string, bool) {
	line, rest, found := strings.Cut(file, "\n")
	if !found {
		return StampInfo{}, file, false
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, stampPrefix) || !strings.HasSuffix(line, stampSuffix) {
		return StampInfo{}, file, false
	}
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(line, stampPrefix), stampSuffix))
	if len(fields) == 0 {
		return StampInfo{}, file, false
	}
	info := StampInfo{Hash: fields[len(fields)-1]}
	if len(fields) > 1 {
		info.Version = fields[len(fields)-2]
	}
	return info, rest, true
}

// ClassifySidecar decides what may be done with an existing sidecar. Pass the
// file exactly as read (empty if absent) and the body we would write now.
//
// The hash is checked BEFORE comparing against the wanted body, so any edit —
// including one that happens to move the file toward the current template —
// classifies as edited rather than stale. Being wrong in that direction costs a
// `--force`; being wrong in the other overwrites someone's work.
func ClassifySidecar(file, want string) SidecarState {
	if file == "" {
		return SidecarMissing
	}
	hash, rest, ok := ParseStamp(file)
	if !ok {
		return SidecarUnverifiable
	}
	if hash != bodyHash(rest) {
		return SidecarEdited
	}
	if rest != want {
		return SidecarStale
	}
	return SidecarCurrent
}

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
	_, after, ok := strings.Cut(s, BeginMarker)
	if !ok {
		return "", false
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, EndMarker)
	if !ok0 {
		// An opening marker with no close means a truncated or hand-edited file.
		// Treated as not installed so a reinstall appends a well-formed block,
		// rather than as a parse error the user cannot act on.
		return "", false
	}
	return before0, true
}

// WithoutBlock returns the file with our delimited block removed.
func WithoutBlock(s string) string {
	before, after, ok := strings.Cut(s, BeginMarker)
	if !ok {
		return s
	}
	rest := after
	_, after0, ok0 := strings.Cut(rest, EndMarker)
	if !ok0 {
		return before
	}
	return before + after0
}

// ReferencesDoc reports whether the developer already loads this document
// themselves, in any path form. A document they reference is THEIRS — it may live
// in a directory we never look at — so we must neither inspect it beside the
// agent's instruction file nor write one there. Doing so would leave a second
// copy that nothing includes, in a package whose rule is that nothing is created
// speculatively.
func ReferencesDoc(existing, name string) bool { return referencesDoc(existing, name) }

// IncludeTarget returns the path a developer's own include points at, exactly as
// they wrote it (`~/.claude/SCTX.md`, `./docs/SCTX.md`, `SCTX.md`).
//
// WHY THE PATH AND NOT JUST THE FACT. Knowing only that a document is referenced
// forced the installer to leave it alone entirely, and the common case is an
// include pointing at the very file `sctx setup` would have written — a
// hand-written `@~/.claude/SCTX.md` beside CLAUDE.md. That file was then never
// inspected, never updated, and reported as `[ok]` while carrying a template two
// releases old: the agent read stale coverage claims forever and nothing said so.
// Returning the path lets the caller resolve it, decide whether it is somewhere
// it may write, and apply the same provenance rules it applies to its own
// sidecars. The decision about WHERE to write stays with the caller, which is the
// only side that can see the filesystem.
func IncludeTarget(existing, name string) (string, bool) {
	for line := range strings.SplitSeq(WithoutBlock(existing), "\n") {
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
			return fields[0], true
		}
	}
	return "", false
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
	_, ok := IncludeTarget(existing, name)
	return ok
}
