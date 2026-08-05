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

Covered today: ` + "`go`" + `, ` + "`git`" + `, ` + "`grep`/`rg`" + `, ` + "`ls`/`find`/`tree`" + `,
` + "`cat`/`head`/`tail`" + `, ` + "`diff`" + `, ` + "`ps`" + `, ` + "`du`" + `, ` + "`make`" + `,
` + "`golangci-lint`" + `, ` + "`gh`" + `, ` + "`docker`" + `, ` + "`kubectl`" + `, ` + "`pytest`" + `,
` + "`ruff`" + `, ` + "`mypy`" + `, ` + "`pip`" + `, ` + "`npm`/`pnpm`/`yarn`" + `, ` + "`brew`" + `,
` + "`mongosh`" + `, ` + "`ssh`" + ` (delegates to the remote command's formatter),
` + "`rsync`" + `, ` + "`jq`/`curl`" + `. ` + "`sctx doctor`" + ` prints the effective list.

**Where the hook declines, and why it matters to you.** It leaves a command
alone when wrapping could change what you conclude:

- a downstream ` + "`grep`/`sed`/`awk`/`wc`/`jq`" + ` — filtering already-compressed
  output would make something look ABSENT that is merely elided;
- file redirects (` + "`> out.txt`" + `), command substitution (` + "`$(…)`" + `), subshells.

` + "`2>&1`" + ` is fine, and so are pure pagers (` + "`| head`" + `, ` + "`| tail`" + `).

## When to type it yourself

**When you are about to run something NOT in the list above and its output will
be long.** ` + "`sctx <cmd>`" + ` still helps: JSON stdout is compacted automatically
and repeated lines are collapsed, whatever the program.

` + "`sctx -- <cmd>`" + ` forces verbatim passthrough when you genuinely need every
byte.

## Reporting

` + "`sctx gain`" + ` shows tokens saved (` + "`--project`" + `, ` + "`--since 7d`" + `,
` + "`--format json`" + `). ` + "`sctx gain --failures`" + ` lists commands that saved
nothing — the fastest way to find an output shape worth compressing.
`
}

func synapctxBody(orgs []string) string {
	scope := `## Which organization you are asking

Every tool takes an optional ` + "`organization`" + `. Pass the one that owns the
repository you are working in — derive it from the working directory
(` + "`.../<organization>/<repository>`" + `). Results never mix organizations.
`
	switch len(orgs) {
	case 0:
	case 1:
		scope = fmt.Sprintf(`## Which organization you are asking

Configured: **%s**. It is the default, so you can omit `+"`organization`"+`.
`, orgs[0])
	default:
		scope = fmt.Sprintf(`## Which organization you are asking

Configured: %s. **Always pass `+"`organization`"+`**, derived from the working
directory (`+"`.../<organization>/<repository>`"+`). Omitting it searches the
default one — a wrong answer rather than an error, which is the harder failure
to notice. Cross-organization questions are allowed but must name the other one
explicitly; results never mix.
`, "**"+strings.Join(orgs, "**, **")+"**")
	}

	return `# SynapCTX — the organization's code graph and memory

SynapCTX indexes **every repository the organization owns**, including ones not
checked out here, and holds durable memory shared with teammates' agents.

Local tools see one checkout and the strings you already thought to search for.
These see the organization, and state how far each answer can be trusted.

` + scope + `
An **organization** is the boundary — its own graph, memory and credentials.
Nothing crosses it. A **project** groups the repositories making up one system
inside one: pass ` + "`project_id`" + ` to ` + "`retrieve_context`" + ` when an
organization runs several unrelated systems. Unclassified repositories are
ALWAYS searched, so a project narrows what is definitely someone else's and
never hides what nobody has classified.

## When to call which

**Before searching for something you cannot name exactly** — a convention, "how
does X work", an unfamiliar package — call ` + "`retrieve_context`" + `: semantic
search over every indexed repository.

**Before renaming, deleting or changing the signature of anything shared** —
call ` + "`find_references`" + ` (every call site) and ` + "`get_dependents`" + ` (every
importing package, organization-wide). Both are exhaustive and report their own
confidence. ` + "`grep`" + ` is neither, and a missed call site in a repository you do
not have checked out is invisible until it breaks.

**Given a bare identifier**, call ` + "`resolve_symbol`" + ` first, then pass what it
returns to the exhaustive tools.

**When you need code from a repository that is not checked out here** — call
` + "`get_source`" + `. Do not clone it.

**Starting unfamiliar work, or before re-deciding something** — call
` + "`recall_memory`" + `. Why a decision was made is rarely recoverable from code.

**The moment a decision is made, a non-obvious constraint is found, or a
convention is set** — call ` + "`store_memory`" + ` with the decision AND the why. One
memory per fact; no session narration, nothing derivable from code or git
history, never secrets. Most worth calling and easiest to skip: the reasoning
exists only at the moment it happens.

## Every answer tells you how far to trust it

Each ` + "`" + `retrieve_context` + "`" + ` result ends with an **answer warrant**. Read it; it is
there so you never have to take a result on faith.

- ` + "`" + `indexed: <repo> @ <sha> (<ref>, <time>)` + "`" + ` — the commit each repository was
  indexed from. **Compare it against your checkout.** Equal means the result
  describes the code in front of you and re-reading the file buys nothing.
  Different means it may not, and reading the file is the right call. This is
  the one claim in the response you can verify independently.
- ` + "`" + `searched N repositories` + "`" + ` — the scope behind this answer. A thin result
  from a narrowed scope reads exactly like an empty index.
- ` + "`" + `ranked, not exhaustive` + "`" + ` — on every answer, because it is always true.
  Nothing absent from a retrieval result licenses "X does not exist". For a
  complete answer use ` + "`" + `find_references` + "`" + ` or ` + "`" + `get_dependents` + "`" + `.
- ` + "`" + `DEGRADED` + "`" + ` — the semantic signal was unavailable; this came from keyword
  and graph alone. Recall is meaningfully worse; treat it as a hint.
- ` + "`" + `truncated` + "`" + ` — matches were dropped to fit ` + "`" + `max_tokens` + "`" + `, not because there
  were none. Raise the budget rather than concluding absence.

## What these cannot see

Ranked search cannot tell you it missed something. The exhaustive tools CAN:
` + "`" + `find_references` + "`" + ` reports ` + "`" + `language_unsupported` + "`" + ` rather than "no callers"
when it cannot analyse a language, because **"no callers" means a change is safe
and "not analysed" means we do not know**.

A result labelled with a project name came from a different project in the same
organization — usually worth knowing, not a mistake.

Uncommitted edits are invisible unless the workspace daemon is running.

If a local tool is the better choice, say which capability was missing.
`
}
