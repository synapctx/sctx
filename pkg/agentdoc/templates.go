package agentdoc

import (
	"fmt"
	"strings"
)

// The two shipped instruction files.
//
// These load into EVERY session's context window, which makes them the one place
// in this product where we pay our own token cost on every single turn. They are
// deliberately short. A four-page instruction file that saves nothing is a
// self-refuting advertisement for a token-optimizing tool.
//
// Wording is trigger-shaped — "when you are about to X, call Y" — because that is
// what was measured to work. Neutral capability descriptions ("Y searches the
// org's code") produced zero calls across five tools in a controlled session,
// while the one tool retaining an explicit trigger was used. Blanket mandates
// ("ALWAYS call Y first") also work and are still refused here: they make usage
// unfalsifiable, so we could never tell a customer's agent chose us on merit.

func sctxBody([]string) string {
	return `# sctx — token-optimized command output

` + "`sctx`" + ` runs a developer command and re-renders its output token-minimally.
It exists because command output is the largest uncontrolled cost in an agent
session: a test run, a ` + "`git log`" + `, a ` + "`kubectl get`" + ` can each spend thousands
of tokens restating things you do not need.

## What it guarantees

Read these once — they are why you can act on compressed output instead of
re-running the command verbatim to check.

- **The exit code is exact.** Never inferred from the text.
- **Error signal is never compressed away.** A failing command keeps its
  diagnostics; compression targets repetition and noise, not failure.
- **Every elision is marked** — ` + "`…+N`" + ` (N more lines) or ` + "`×N`" + ` (repeated N
  times). If you see no marker, nothing was dropped.
- **Any parse failure degrades to raw output.** Tiers fall back
  aggressive → relaxed → verbatim. Output is never suppressed, so an unexpected
  format costs you nothing.

## You do not need to type it

A PreToolUse hook rewrites covered commands automatically. **Write commands
naturally** — including inside pipelines and ` + "`&&`" + ` sequences. Do not prefix
` + "`sctx`" + ` yourself on a covered command: it is not double-wrapped, but the
token is wasted and the command reads as though it needed help.

Wrapped today: ` + "`go`" + `, ` + "`git`" + `, ` + "`grep`/`rg`" + `, ` + "`ls`/`find`/`tree`" + `,
` + "`cat`/`head`/`tail`" + `, ` + "`diff`" + `, ` + "`ps`" + `, ` + "`du`" + `, ` + "`df`" + `,
` + "`make`" + `, ` + "`golangci-lint`" + `, ` + "`gh`" + `, ` + "`docker`" + `, ` + "`kubectl`" + `,
` + "`helm`" + `, ` + "`aws`/`gcloud`/`az`" + `, ` + "`terraform`/`tofu`/`pulumi`" + `,
` + "`pytest`" + `, ` + "`ruff`" + `, ` + "`mypy`" + `, ` + "`pip`/`uv`/`poetry`" + `,
` + "`npm`/`pnpm`/`yarn`" + `, ` + "`cargo`" + `, ` + "`dotnet`" + `, ` + "`mvn`/`gradle`" + `,
` + "`composer`" + `, ` + "`bundle`" + `, ` + "`tsc`/`eslint`" + `, ` + "`brew`" + `,
` + "`systemctl`/`journalctl`" + `, ` + "`mongosh`" + `, ` + "`rsync`" + `, ` + "`jq`/`curl`" + `,
` + "`ssh`" + ` (delegates to the remote command's formatter).

**Coverage is per (program, SUBCOMMAND)**, which is why a command you expected to
be wrapped sometimes is not — a program is rewritten only for the subcommands it
has a formatter for. Plumbing verbs pass through raw (` + "`git rev-parse`" + `,
` + "`go env`" + `); their output is a line or two, so "no compression markers" does not
mean sctx is broken. Programs whose first argument is a path or host
(` + "`grep`" + `, ` + "`ls`" + `, ` + "`cat`" + `, ` + "`curl`" + `, ` + "`ssh`" + `) take no
subcommand and are always wrapped.

**Where the hook declines, and why it matters to you.** It leaves a command
alone when wrapping could change what you conclude:

- a downstream ` + "`grep`/`sed`/`awk`/`wc`/`jq`" + ` — filtering already-compressed
  output would make something look ABSENT that is merely elided;
- file redirects (` + "`> out.txt`" + `), command substitution (` + "`$(…)`" + `), subshells.

` + "`2>&1`" + ` is fine, and so are pure pagers (` + "`| head`" + `, ` + "`| tail`" + `).

## When to type it yourself

**When you are about to run something NOT in the list above and its output will
be long.** ` + "`sctx <cmd>`" + ` still helps, whatever the program: JSON stdout is
compacted, and runs of repeated lines — the progress and status spam most tools
emit — collapse to one line plus a count. Nothing is summarised and nothing is
guessed at, so an unrecognised format costs you nothing.

` + "`sctx -- <cmd>`" + ` forces verbatim passthrough when you genuinely need every
byte.

## The subcommand that changes what SynapCTX can answer

` + "`sctx watch`" + ` streams the structural diff of the working tree — symbol names,
signatures, doc comments, never bodies — so ` + "`retrieve_context`" + ` answers about
the code being CHANGED rather than the last commit. Without it, uncommitted edits
are invisible to every SynapCTX tool, and a result will confidently describe the
committed version of a function just rewritten.

It is foreground and per-developer. Suggest it when a session has substantial
uncommitted work — but do not start a long-running foreground process on the
developer's behalf without asking.

` + "`sctx doctor`" + ` shows CONFIGURATION — version, masked per-org API-key prefixes,
the default organization, and which agents here have been taught. It does not
print the covered-command list.

## Reporting

` + "`sctx gain`" + ` shows tokens saved (` + "`--project`" + `, ` + "`--since 7d`" + `,
` + "`--format json`" + `). ` + "`sctx gain --failures`" + ` lists commands that saved
nothing — the fastest way to find an output shape worth compressing.
`
}

func synapctxBody(orgs []string) string {
	// THE CREDENTIAL DECIDES, NOT THE ARGUMENT. This block used to say "always
	// pass `organization`", which holds only for a key whose principal is a
	// member of every organization it names. With one key PER organization —
	// what `sctx init` produces, and the shipped topology — naming a different
	// one is REFUSED, and the refusal is deliberately indistinguishable from "no
	// such organization" so it cannot be used to enumerate them. Agents read
	// that as a broken index and went hunting one; observed repeatedly in the
	// field, which is what this wording exists to stop.
	scope := `## Which organization you are asking

Every tool takes an optional ` + "`organization`" + `, but your API key decides which
one answers. Omit it to search the key's own; naming one the key does not cover
is refused. Results never mix organizations.
`
	switch len(orgs) {
	case 0:
	case 1:
		scope = fmt.Sprintf(`## Which organization you are asking

Configured: **%s**. It is your key's own organization, so omit `+"`organization`"+`.
`, orgs[0])
	default:
		var routes strings.Builder
		for _, org := range orgs {
			// MCP server names retain the organization slug, while Codex
			// normalizes punctuation in the generated tool namespace.
			codexOrg := strings.ReplaceAll(org, "-", "_")
			fmt.Fprintf(&routes, "- **%s** → server `synapctx-%s`; Codex namespace `mcp__synapctx_%s__*`\n", org, org, codexOrg)
		}
		scope = fmt.Sprintf(`## Which organization you are asking

Each configured organization has its own server and API key:

%s
Choose the server/namespace matching the organization that owns the working
directory (`+"`.../<organization>/<repository>`"+`) and OMIT `+"`organization`"+`.
For cross-organization work, switch namespaces for each call; one session may
use several. The selected server's key, not the `+"`organization`"+` argument,
decides which organization answers.

Naming one your key does not cover is refused as "unknown or inactive" — the same
wording as a genuinely missing org, so it cannot enumerate them. **Read it as
"wrong key", not "broken index"**: it names the org your key IS scoped to.
`, routes.String())
	}

	// WHAT BELONGS HERE, AND WHAT DOES NOT. This file and the MCP tool
	// descriptions are BOTH sent at the start of every session, and they used to
	// say ~55% of the same things — the customer paying twice for one sentence.
	// The placement rule, so a future author does not re-merge them:
	//
	//   constrains ONE tool's inputs or when to call it  -> its description
	//   qualifies an ANSWER (scope, ref, confidence)     -> rendered on the answer
	//   is an ACTION                                     -> a tool
	//   machine-local fact, or which tool to reach for   -> HERE
	//
	// If something seems to need two homes it is an answer-qualification dressed
	// as doctrine; put it on the answer, where it arrives attached to the result
	// it qualifies and cannot be forgotten by hour four.
	//
	// What stays here is what nothing else can deliver: that these tools EXIST
	// (measured: 7.6x more invocation when this file is referenced), the triggers
	// for reaching for them, and the credential routing, which depends on this
	// machine's configuration and so cannot live in a shipped description.
	return `# SynapCTX — the organization's code graph and memory

SynapCTX indexes **every repository the organization owns**, including ones not
checked out here, and holds durable memory shared with teammates' agents.

Local tools see one checkout and the strings you thought to search for. These see
the organization, and state how far each answer can be trusted.

` + scope + `
Some clients defer large tool catalogs. If a named tool is not initially
visible, search or list the client's deferred tools for the selected server
before falling back. Not initially displayed does not mean unavailable.

## When to reach for it

- **Before searching for something you cannot name exactly** — a convention, "how
  does X work", an unfamiliar package: ` + "`retrieve_context`" + `.
- **Before renaming, deleting or changing a shared signature** —
  ` + "`find_references`" + ` and ` + "`get_dependents`" + `. They are exhaustive; ` + "`grep`" + ` is
  not, and a call site in a repository you have not checked out is invisible
  until it breaks.
- **To verify one symbol, or to read code from a repository not checked out
  here** — ` + "`get_symbol_source`" + ` and ` + "`get_source`" + `. Do not clone it.
- **Before changing or retiring a service boundary** —
  ` + "`get_service_dependencies`" + ` shows upstream and downstream services.
- **When looking for routes that may be safe to remove** —
  ` + "`find_unused_endpoints`" + ` produces a shortlist, not deletion authorization;
  read its blind spots before acting.
- **Starting unfamiliar work, or before re-deciding something** —
  ` + "`recall_memory`" + `. Why a decision was made is rarely recoverable from code.
- **The moment a decision is made or a convention is set** — ` + "`store_memory`" + `,
  with the decision AND the why. It outlives this session and this machine, and
  every teammate's agent can recall it.
- **When a memory is outdated** — write the replacement with ` + "`store_memory`" + `
  and mark what it supersedes. Use ` + "`forget_memory`" + ` only for secrets or test
  artifacts, not ordinary history.

## Read what each answer says about itself

Answers carry their own limits: what was searched, which ref, what was dropped,
whether the signal degraded, whether a billing limit stopped the result. Those
lines appear only when they apply, they are complete sentences, and each states
its own next action — so read them rather than the summary alone.

Nothing absent from a ranked answer licenses "it does not exist". The exhaustive
tools are the ones that can settle that, and they say plainly when they cannot.

**Uncommitted edits are invisible unless ` + "`" + `sctx watch` + "`" + ` is running** — the reason
retrieval may describe the committed shape of code you just rewrote.

If a local tool is the better choice, say which capability was missing.
`
}
