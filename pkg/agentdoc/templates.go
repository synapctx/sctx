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

` + "`sctx`" + ` runs developer commands and renders their output token-minimally.

## What it guarantees

- Exit codes are exact; failure diagnostics remain.
- Elisions are marked ` + "`…+N`" + ` or ` + "`×N`" + `; no marker means nothing was dropped.
- Parse failure falls back aggressive → relaxed → verbatim; output is never suppressed.

## Whether you have to type it depends on your agent

` + "`sctx setup --install`" + ` wraps commands automatically wherever the client gives it
somewhere to intervene:

- **Claude Code**, **Codex** (PreToolUse hook) and **Gemini CLI** (BeforeTool hook)
- **Kilo Code** and **OpenCode** (a plugin on ` + "`tool.execute.before`" + `)

In those five, write commands naturally — including pipelines and ` + "`&&`" + ` — and do
not prefix ` + "`sctx`" + ` yourself. Codex is the exception worth knowing: it will not
run a hook until a human trusts it once with ` + "`/hooks`" + `, so until then nothing is
wrapped there and prefixing is worth it.

**No interception point exists in Windsurf or Crush**, so nothing rewrites
anything there: prefix the covered commands yourself (` + "`sctx go test ./...`" + `,
` + "`sctx git status`" + `). Prefixing when something already wrapped it is harmless —
an already-wrapped command is detected and never wrapped twice — so when in
doubt, prefix.

Worth prefixing (and what is wrapped for you where that is automatic): ` + "`go`" + `, ` + "`git`" + `, ` + "`grep`/`rg`" + `, ` + "`ls`/`find`/`tree`" + `,
` + "`cat`/`head`/`tail`" + `, ` + "`diff`" + `, ` + "`ps`" + `, ` + "`du`" + `, ` + "`df`" + `,
` + "`make`" + `, ` + "`golangci-lint`" + `, ` + "`gh`" + `, ` + "`docker`" + `, ` + "`kubectl`" + `,
` + "`helm`" + `, ` + "`aws`/`gcloud`/`az`" + `, ` + "`terraform`/`tofu`/`pulumi`" + `,
` + "`pytest`" + `, ` + "`ruff`" + `, ` + "`mypy`" + `, ` + "`pip`/`uv`/`poetry`" + `,
` + "`npm`/`pnpm`/`yarn`" + `, ` + "`cargo`" + `, ` + "`dotnet`" + `, ` + "`mvn`/`gradle`" + `,
` + "`composer`" + `, ` + "`bundle`" + `, ` + "`tsc`/`eslint`" + `, ` + "`brew`" + `,
` + "`systemctl`/`journalctl`" + `, ` + "`mongosh`/`sqlite3`/`dig`/`psql`" + `, ` + "`rsync`" + `, ` + "`jq`/`curl`" + `,
` + "`ssh`" + `, ` + "`docker exec`" + ` and ` + "`kubectl exec`" + ` delegate to the inner
command's own formatter, so ` + "`docker exec web go test ./...`" + ` is rendered as
` + "`go test`" + `. Interactive ones (` + "`-it`" + `) are left alone.

Generic-only: cloud/IaC/build rows, ` + "`systemctl`/`journalctl`" + `, ` + "`df`" + ` and
` + "`jq`/`curl`/`sqlite3`" + `; unknown shapes stay raw.

Coverage is per (program, subcommand). Uncovered plumbing such as
` + "`git rev-parse`" + ` and ` + "`go env`" + ` stays raw. Path/host-first commands
(` + "`grep`" + `, ` + "`ls`" + `, ` + "`cat`" + `, ` + "`curl`" + `, ` + "`ssh`" + `) are always wrapped.

Wrapping is refused — and you should not add it by hand either — when it could
change the conclusion:

- downstream ` + "`grep`/`sed`/`awk`/`wc`/`jq`" + ` (an elided match could look absent);
- file redirects (` + "`> out.txt`" + `), command substitution (` + "`$(…)`" + `), subshells.

` + "`2>&1`" + ` is fine, and so are pure pagers (` + "`| head`" + `, ` + "`| tail`" + `).

## When to type it yourself

**When an unlisted command may be long**, use ` + "`sctx <cmd>`" + `: JSON is compacted,
repeated lines collapse, and a JSONL/NDJSON stream keeps both ends with a counted
marker between them. Unrecognized output stays raw.

` + "`sctx -- <cmd>`" + ` forces verbatim passthrough when you genuinely need every
byte.

## The subcommand that changes what SynapCTX can answer

` + "`sctx watch`" + ` streams uncommitted symbol/signature/doc changes so
` + "`retrieve_context`" + ` sees the code being changed, not only the last commit.

It is foreground and per-developer; suggest it for substantial edits, but ask
before starting it.

` + "`sctx doctor`" + ` shows configuration, masked key prefixes, default org and
taught agents—not command coverage. ` + "`sctx setup`" + ` reports, per agent on this
machine, whether the instructions, the MCP servers and the rewrite hook are
actually in place.

` + "`sctx: raw output: PATH (TTL)`" + ` points to expiring, byte-exact local
` + "`stdout`/`stderr`" + `; read only if an omitted detail is needed.

Trusted ` + "`.sctx/filters.json`" + ` rules need external digest approval.
` + "`sctx filters verify`" + ` and ` + "`sctx filters status`" + ` are safe to run and say
whether the rules are active; never run ` + "`sctx filters trust`" + ` for the developer.

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
	//
	// ONE DELIBERATE EXCEPTION to the placement rule, 2026-09-03: the
	// session-opening trigger ("Open every task with it") is carried BOTH here
	// and in the MCP server's `instructions` field (developer-mcp-proxy). That is
	// duplication and it was priced as such. It stays because `instructions` is
	// advisory — not every client renders it, and a client that drops it drops
	// the ONE sentence that decides whether the tools are reached for at all,
	// with no symptom to notice. The owner ruled that completeness of this
	// guidance outranks its token cost. Do not "fix" it by deleting either copy;
	// if a future client survey shows every target renders `instructions`, delete
	// THIS copy and record the survey.
	return `# SynapCTX — the organization's code graph and memory

SynapCTX indexes every repository in the organization and shared durable memory.

` + scope + `
Some clients defer large tool catalogs, listing these tools as deferred names
without schemas. A deferred tool is not unavailable: search or list the client's
deferred tools for that server, load it, and call it.

## Open every task with it

**When a task starts** — before the first grep, glob or file read, whether or
not the code feels familiar — make two calls, once each:

1. ` + "`recall_memory`" + ` with the task in your own words: the decisions, pitfalls and
   open work about this code that no file records.
2. ` + "`retrieve_context`" + ` with the same task: the whole organization's code graph,
   every repository at once, checked out or not.

Then use local tools for exact bytes. Familiarity is not a reason to skip this;
the memory that matters most is the one you do not know you are missing.

## Three memory systems, one of them shared

Personal memory (an agent's own notes, a session-continuity plugin) is PUSHED
into context. SynapCTX memory is the ORGANIZATION'S and is PULLED: personal
notes arriving does not mean org memory was checked — only ` + "`recall_memory`" + ` does.
A decision, convention, root cause or lesson goes to ` + "`store_memory`" + ` with its
why; supersede rather than forget.

A verified stamp shows someone recently re-confirmed a memory; an aging
warning flags an unconfirmed infra detail (host, port, path, flag) past
thirty days. Check the live system, then re-confirm with
` + "`{verifies: <id>}`" + ` or replace it with
` + "`{verifies: <new id>, supersedes: <old id>}`" + `.

## Also reach for it

- **Before renaming, deleting or changing a shared signature** —
  ` + "`find_references`" + ` and ` + "`get_dependents`" + `; unlike ` + "`grep`" + `, they cross repositories.
- **To verify a symbol or read an absent checkout**: ` + "`get_symbol_source`" + ` or ` + "`get_source`" + `.
- **Before changing a service boundary**: ` + "`get_service_dependencies`" + `.
- **When assessing removable routes**: ` + "`find_unused_endpoints`" + `; it is a shortlist.
- **When memory is outdated**: supersede it with ` + "`store_memory`" + `; use
  ` + "`forget_memory`" + ` only for secrets or test artifacts.

## Read what each answer says about itself

Answers carry their own limits and next actions; read them, not only the summary.

Nothing absent from a ranked answer licenses "it does not exist".

**Uncommitted edits are invisible unless ` + "`" + `sctx watch` + "`" + ` is running** — the reason
retrieval may describe the committed shape of code you just rewrote.

If a local tool is the better choice, say which capability was missing.
`
}
