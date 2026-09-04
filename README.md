# sctx

[Home](README.md) &nbsp;/

&nbsp;

**Token-optimizing command wrapper for AI coding agents.**

`sctx` runs the commands your agent already runs and returns the same
information in a fraction of the tokens - exit code intact, errors intact,
every omission marked.

For more details and guides, please visit [**synapctx.com/sctx**](https://synapctx.com/sctx/)

&nbsp;

[![SynapCTX](https://img.shields.io/badge/SynapCTX-sctx-2f6feb)](https://synapctx.com/sctx/)
[![CI](https://github.com/synapctx/sctx/actions/workflows/ci.yaml/badge.svg)](https://github.com/synapctx/sctx/actions/workflows/ci.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/synapctx/sctx.svg)](https://pkg.go.dev/github.com/synapctx/sctx)
[![GitHub Tag](https://img.shields.io/github/v/tag/synapctx/sctx?label=Version)](https://github.com/synapctx/sctx/tags)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE.txt)

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Overview

An AI coding agent reads with its context window, and command output is the
largest thing it reads that nobody chose. A test run that prints four hundred
passing lines, a `git status` padded with hints, a recursive grep returning the
same file forty times - all of it arrives in full, is paid for in full, and
almost none of it is what the agent needed.

`sctx` sits in front of those commands. It parses the output it recognises and
re-renders it around the part that carries the signal: the failures, the changed
files, the matches grouped by location. Everything else is summarised behind an
explicit marker, so the agent can always tell that something was left out.

&nbsp;

**Measured across 6,361 real command runs: 61% of output tokens removed.**

| Command | Output tokens removed | Runs measured |
| :--- | ---: | ---: |
| `rg` | **91.4%** | 436 |
| `go test` | **58.2%** | 613 |
| `grep` | **52.6%** | 2,209 |
| `find` | **48.1%** | 141 |
| `git diff` | **26.0%** | 47 |
| `ls` | **23.3%** | 319 |
| `cat` | **0.5%** | 127 |
| `make` | **0.0%** | 88 |

&nbsp;

This table is a **snapshot**, taken 2026-08-04. The figures move as the ledger
grows, and a number written into a README stops moving the moment it is
committed - the last snapshot here sat at 46.3% long after the measurement had
risen. For the current numbers, refreshed hourly from the same telemetry:

**→ [synapctx.com/sctx](https://synapctx.com/sctx/)**

&nbsp;

The last two rows are the design working, not a shortfall. When output holds no
redundancy - `cat` of a source file, a short `make` run - `sctx` returns it byte
for byte. It compresses what is genuinely repetitive and steps out of the way
everywhere else, which is why a file dump saves nothing and a recursive `rg`
saves almost everything.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

### Why a wrapper rather than a bigger context window

Because the cost is not only money. A context window filled with passing test
lines is a context window that no longer holds the file the agent is editing,
and quality degrades long before the window is full. Removing output the agent
never needed buys attention, not just budget.

And because correctness is not negotiable in this position. A wrapper sits
between a developer's command and its result, so `sctx` is built to fail
harmlessly: if a renderer cannot parse something, you get the original bytes;
if a command fails, the diagnostics survive and only the noise shrinks. It never
returns something an agent could mistake for the whole picture.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

### Where SynapCTX comes in

`sctx` is free, MIT-licensed, and complete on its own. Install it, and it works.

It is also the local half of [SynapCTX](https://synapctx.com/), a context engine
for engineering organizations. Connect an account and `sctx` gains a second job:
reporting what it saved, and keeping your working tree visible to the platform's
retrieval.

**For an individual developer**, that means your agent can ask about code across
every repository you work in - not just the one that is open - and get answers
about the version in front of you rather than the last commit.

**For an organization**, it means the same knowledge graph and the same memory
serve every developer's agent. Savings roll up per repository and per developer.
Decisions recorded once are recalled by a teammate's agent months later. And
questions that only a whole-estate view can answer - every caller of a function,
every consumer of an endpoint, across repositories nobody has checked out -
become answerable rather than guessed at.

The boundary is deliberate: nothing in this repository requires an account, and
nothing about `sctx` degrades without one. What an account adds, and the live
savings figures, are on [synapctx.com/sctx](https://synapctx.com/sctx/).

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Quick look

```bash
sctx go test ./...      # the failures and the counts, not 400 passing lines
sctx git status         # the changed files, not the hints
sctx grep -rn foo .     # matches grouped by file, with explicit +N markers
sctx gain               # what it has saved you so far
```

Nothing else changes. The exit code is the command's own, stdout and stderr keep
their meaning, and a pipeline behaves as it did before.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Install

**Homebrew** - macOS and Linux:

```bash
brew install synapctx/tap/sctx
```

**Go** - any platform with a Go 1.26+ toolchain:

```bash
go install github.com/synapctx/sctx/cmd/sctx@latest
```

**Binary** - download the archive for your platform from
[Releases](https://github.com/synapctx/sctx/releases).

macOS and Linux:

```bash
tar -xzf sctx_<version>_<os>_<arch>.tar.gz
sudo install -m 0755 sctx sctxd /usr/local/bin/
```

Windows (PowerShell):

```powershell
Expand-Archive sctx_<version>_windows_amd64.zip -DestinationPath $env:LOCALAPPDATA\Programs\sctx
# then add that directory to your PATH
```

Prebuilt archives cover **macOS** (Apple Silicon, Intel), **Linux** (x86-64,
arm64) and **Windows** (x86-64, arm64). They are statically linked and depend on
nothing at runtime.

Keep `sctx` and `sctxd` in the same directory. `sctx watch` looks for its helper
beside its own executable, so a matched pair always wins over an older copy
elsewhere on `PATH`.

&nbsp;

Confirm the install and see the effective configuration:

```bash
sctx version
sctx doctor
```

&nbsp;

**Upgrading**

```bash
# Homebrew
brew upgrade sctx

# Go
go install github.com/synapctx/sctx/cmd/sctx@latest
```

Configuration and the local savings ledger live in `~/.config/sctx` and are
untouched by an upgrade.

&nbsp;

**Uninstalling**

```bash
# Optional: removes the savings ledger and any API keys
brew uninstall sctx
rm -rf ~/.config/sctx
```

&nbsp;

### Windows

`sctx` itself works identically on every platform: it is a single static
binary, and the wrapped command's exit code and output are unchanged.
`sctx setup --install` writes every hook as a plain **command string** —
never a shell script — because Claude Code and the other clients below spawn
that string directly, with no bash available on Windows to run one.

Verified by a `windows-latest` CI job on every change (not just tested once by
hand), `sctx setup --install` wires auto-wrap for:

- **Claude Code** — all four hooks (`settings.json`)
- **OpenAI Codex CLI** — the `PreToolUse` hook (`config.toml`); Codex still
  requires a one-time `/hooks` trust step before it runs, same as elsewhere
- **Gemini CLI** — the `BeforeTool` hook (`settings.json`)
- **Kilo Code** — the auto-wrap plugin (`%USERPROFILE%\.config\kilo\plugin\sctx.js`)

**Untested on Windows by CI** (the code path is the same, cross-compiled and
`go vet`-clean, but nothing exercises it on the platform yet): Cursor,
GitHub Copilot CLI, Factory Droid, and `sctx watch`'s `sctxd` helper process.

Configuration and the local savings ledger live under
`%USERPROFILE%\.config\sctx` — the same relative layout as `~/.config/sctx`
elsewhere, chosen because that is also where Claude Code, Codex and Gemini
already keep their own configuration on Windows (`~\.claude`, `~\.codex`,
`~\.gemini`).

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Getting started

`sctx` works the moment it is installed - put it in front of any command:

```bash
sctx go build ./...
```

To have it applied automatically, run:

```bash
sctx setup
```

&nbsp;

This detects which AI coding agents are present, and for each one adds a short
instruction file describing what `sctx` is and when to use it. For Claude Code it
also registers a hook, so commands are wrapped as they are issued: you and your
agent keep writing `go test ./...`, and the compact output is what arrives. When
you have connected a SynapCTX account, it also registers every configured
organization as a Streamable HTTP MCP server for OpenAI Codex. Instructions and
tool access are checked separately, so setup cannot report Codex ready while its
MCP server list is empty.

```bash
sctx setup                 # report what is installed, and what is missing
sctx setup --install       # apply it
sctx setup --list-agents   # every agent sctx knows how to configure
sctx setup --agent <id>    # configure one explicitly
```

For Claude Code specifically, `sctx setup --install` registers four hooks. They
fail independently, so `sctx setup` reports each one separately:

| Hook | Event (matcher) | What it does |
| :--- | :--- | :--- |
| `sctx hook claude` | `PreToolUse` (`Bash`) | rewrites covered commands to `sctx <cmd>` — this is what produces the savings |
| `sctx hook claude-session-start` | `SessionStart` (`startup\|resume\|clear\|compact`) | briefs the agent before it reads anything: org memory bound to this repository, how fresh the index is against your local `HEAD`, and the tools to open with |
| `sctx hook claude-first-search` | `PreToolUse` (`Grep\|Glob\|Agent`) | on the first two local searches of a session, points at the organization-wide graph and memory; then stays quiet |
| `sctx hook claude-post-tool` | `PostToolUse` (`Edit\|Write\|Bash`) | surfaces org memory about a file you just edited, and the cross-repository call sites a grep could not see |

The session-start and first-search hooks name the MCP tools in the namespace
**this machine** uses. To learn it, `~/.claude.json` is READ — never written —
and the server whose `Authorization` header carries this organization's key is
the one named. If it cannot be identified, the tool names are phrased as prose
rather than guessed, because a tool name an agent cannot call teaches it to
distrust everything else the hook said. The API key is never printed or logged.

Two rules govern what it writes. It only adds configuration where an agent has
already established its own, so nothing appears for a tool you do not use. And
it never overwrites content you have edited - a customised instruction file is
left as you wrote it, and only a missing reference is repaired.

Each document it writes begins with a provenance comment recording the sctx that
wrote it and a hash of the body, which is what lets an upgrade bring an untouched
document current while leaving an edited one alone. If you load a document
through your own include - `@~/.claude/SCTX.md` in `CLAUDE.md`, for instance -
that path is followed and kept current there. Includes resolving outside your
home directory, or into a directory that does not exist, are reported and never
written to. A document sctx cannot prove it wrote is reported too, with the
one-time `sctx setup --install --force` that adopts it.

Three more agents get the same rewrite hook, each in its own config file and
its own envelope:

| Agent | Minimum version | Config file | Event (matcher) | Output field |
| :--- | :--- | :--- | :--- | :--- |
| Cursor | 1.7 | `~/.cursor/hooks.json` | `preToolUse` (`"Shell"`) | `updated_input` (Cursor's own envelope — `permission`/`updated_input`, never `hookSpecificOutput`) |
| GitHub Copilot CLI | 1.0.73 | `~/.copilot/hooks/sctx-rewrite.json` | `PreToolUse` | `updatedInput` (Claude-shaped `tool_input`; a documented camelCase `toolName`/`toolArgs` input is also decoded and answered with `modifiedArgs`) |
| Factory Droid | 0.164.0 | `~/.factory/hooks.json` | `PreToolUse` (`Execute`) | `updatedInput`, same envelope as Claude Code |

Droid additionally honours its own `commandDenylist`/`commandBlocklist` entries
(read from every settings scope Droid itself reads): a command already on one
of those lists is left untouched rather than rewritten, so Droid's own
confirm/block flow still sees the command it was written to match.

Codex MCP entries live in a clearly marked block in `~/.codex/config.toml`.
Everything outside that block is preserved, the file is kept mode `0600`, and a
same-named registration outside the block is reported as a conflict rather than
overwritten. The block contains the organization API keys already held in
`~/.config/sctx/config.toml`; setup output and status never print them.

Restart the agent after setup changes anything. For the VS Code Codex extension,
restart the extension (or VS Code) before opening the next session; a new chat in
an already-running extension may still use the old MCP inventory.

If no agent is detected, `sctx setup` reports what it looked for and writes
nothing.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Command coverage

Dedicated renderers:

`go` &nbsp;·&nbsp; `git` &nbsp;·&nbsp; `grep` / `rg` &nbsp;·&nbsp;
`ls` / `find` / `tree` &nbsp;·&nbsp; `cat` / `head` / `tail` &nbsp;·&nbsp;
`diff` &nbsp;·&nbsp; `ps` &nbsp;·&nbsp; `du` &nbsp;·&nbsp; `make` &nbsp;·&nbsp;
`docker` &nbsp;·&nbsp; `kubectl` &nbsp;·&nbsp; `gh` &nbsp;·&nbsp;
`golangci-lint` &nbsp;·&nbsp; `pytest` &nbsp;·&nbsp; `ruff` &nbsp;·&nbsp;
`mypy` &nbsp;·&nbsp; `pip` &nbsp;·&nbsp; `npm` / `pnpm` / `yarn` &nbsp;·&nbsp;
`brew` &nbsp;·&nbsp; `mongosh` &nbsp;·&nbsp; `dig` &nbsp;·&nbsp; `psql` &nbsp;·&nbsp; `rsync` &nbsp;·&nbsp;
`ssh`

Generic shape detection, not dedicated command parsers:

`jq` / `curl` / `sqlite3` &nbsp;·&nbsp; `aws` / `gcloud` / `az` &nbsp;·&nbsp;
`terraform` / `tofu` / `pulumi` &nbsp;·&nbsp; `helm` &nbsp;·&nbsp; `cargo` &nbsp;·&nbsp;
`dotnet` &nbsp;·&nbsp; `mvn` / `gradle` &nbsp;·&nbsp; `composer` / `bundle` &nbsp;·&nbsp;
`uv` / `poetry` &nbsp;·&nbsp; `tsc` / `eslint` &nbsp;·&nbsp;
`systemctl` / `journalctl` &nbsp;·&nbsp; `df`

A covered command whose formatter finds a shape it does not recognise is not
left at full size either: its output still reaches a lossless JSON compaction
pass before falling back to verbatim. That pass changes whitespace only, so the
document every parser sees is identical - `git show HEAD:swagger.json` went from
46,164 to 21,846 bytes with the JSON provably unchanged.

These are wrapped so the generic formatter can compact valid JSON/NDJSON and
collapse provably repeated lines. A long NDJSON stream keeps its opening and its
closing records with an exact count of what was left out between them - the end
of a stream is where a log puts the failure. Unique or unrecognised output
remains verbatim. They are reported as `(generic)`, never as dedicated coverage.

`ssh` is a special case worth knowing about. Its output is whatever ran on the
far end, so `sctx` reads the remote command from the invocation and applies that
command's renderer - `sctx ssh <host> '<command>'` compresses as though the
command had run locally. It declines for interactive sessions, and for anything
compound, where two programs' output cannot be rendered as one.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Guarantees

These are the properties that make a wrapper safe to leave in place, and each is
enforced by tests rather than convention.

| | |
| :--- | :--- |
| **Exit codes are exact** | never inferred from the text, never rewritten |
| **Errors survive compression** | when a command fails, the noise shrinks and every diagnostic remains |
| **Elisions are always marked** | `…+12 more`, `×3`. No marker means nothing was removed |
| **Failure degrades, never suppresses** | an unparseable format falls back to the original output; so does an internal error |
| **Savings are measured conservatively** | the figure in `sctx gain` is a floor, not a flattering estimate |
| **Nothing blocks your command** | accounting and reporting are off the critical path and cannot delay or fail a run |

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Commands

| Command | Purpose |
| :--- | :--- |
| `sctx <cmd> [args...]` | run a command with token-optimized output |
| `sctx -- <cmd>` | run a command that shares a name with an `sctx` subcommand |
| `sctx gain` | savings report, overall and per command |
| `sctx gain --share` | sanitised, copy-pasteable savings card (aggregate numbers only) |
| `sctx bench` | reproducible local benchmark against the current repository |
| `sctx setup` | configure the AI coding agents you use |
| `sctx doctor` | show the effective configuration |
| `sctx init` | connect this installation to a SynapCTX account |
| `sctx watch` | keep uncommitted code visible to your agent (requires an account) |
| `sctx telemetry` | inspect or change what is shared |
| `sctx filters verify` | validate project-local filters and their inline fixtures |
| `sctx filters trust --yes` | approve the exact current project-filter digest |
| `sctx flush` | send any queued usage events now |
| `sctx version` | print the version |

`sctx gain` accepts `--project` to scope to the current repository, `--since`
for a time window, and `--format json` for machine-readable output. `sctx
gain --share` prints a sanitised, copy-pasteable savings card instead of the
full report — aggregate numbers only, never argv or a path — in plain text
or `--format markdown`.

### Trusted project-local filters

Internal CLIs and project-specific `make` targets can declare conservative
line filters in `.sctx/filters.json`. The format cannot run shell commands or
regular expressions: it supports exact argv prefixes, exact/prefix line
removal, and repeated-line collapsing. Failed commands always remain verbatim,
and every removed line is represented by an exact count.

```json
{
  "version": 1,
  "filters": [{
    "id": "make-lint",
    "command": "make",
    "args_prefix": ["lint"],
    "finite": true,
    "override_builtin": true,
    "drop_line_prefixes": ["checking cached module "],
    "collapse_repeats": true,
    "fixtures": [{
      "name": "native successful lint",
      "stdout": "checking cached module a\nchecking cached module b\nchecking cached module c\nchecking cached module d\nchecking cached module e\nchecking cached module f\nchecking cached module g\nchecking cached module h\nlint passed\n",
      "applied": true,
      "expected_stdout": "lint passed\n…+8 lines filtered by project rule make-lint"
    }]
  }]
}
```

Run `sctx filters verify`, inspect the file and its fixture results, then run
`sctx filters trust --yes`. Trust is stored outside the repository and bound to
both the checkout path and SHA-256 digest. Any edit disables the filters until
the new content is explicitly approved. Built-in formatters remain
authoritative unless a trusted rule sets `override_builtin`. Every rule must
also assert `finite: true`; streaming/watch/server commands must never be
buffered behind a project filter.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Benchmark

`sctx bench` is a reproducible, local-only benchmark: it detects the current
repository's language by the files present (a `go.mod` at the root means Go;
anything else gets a language-agnostic generic set) and runs a **fixed,
documented** command set against it twice — once raw, once through sctx's own
pipeline in-process (the same registry and tier chain a real wrapped run
uses, never a second `sctx` subprocess) — then prints the raw-vs-rendered
token comparison. Nothing it measures is sent anywhere: no telemetry emitter
is constructed, and the stats store it hands the pipeline is discarded when
the command returns. Non-zero exits from a benchmarked command are recorded,
never fatal to the run. By default the output never contains a file path —
only program names — unless you pass `--verbose`.

The Go set: `go build ./...`, `go vet ./...`, `go test ./...`, `git status`,
`git log --oneline -n 50`, `git diff HEAD~1`,
`grep -rn "func " --include=*.go .`, `ls -la`, `find . -name "*.go"`. The
generic set: `git status`, `git log --oneline -n 50`, `git diff HEAD~1`,
`ls -la`, `find . -maxdepth 2`.

```shell
sctx bench --name sctx
```

A real run on this repository (2026-09-04, darwin/arm64, sctx dev):

```text
sctx bench
repository:  sctx
language:    go
machine:     darwin/arm64, sctx dev

Command                             Raw   Rendered    Saved Tier       Exit
go build                              0          0     0.0% verbatim   0
go vet                                0          0     0.0% verbatim   0
go test                            1.0K         13    98.7% aggressive 0
git status                          147         32    78.2% aggressive 0
git log                             734        493    32.8% aggressive 0
git diff                           4.3K       3.5K    19.3% aggressive 0
grep                              47.1K       3.4K    92.7% aggressive 0
ls                                  253         88    65.2% aggressive 0
find                               4.0K       1.5K    62.1% aggressive 0

TOTAL                             57.5K       9.0K    84.3%

tokens = bytes/4, a floor
```

`sctx bench --format json` emits the same numbers as a stable, versioned
document (`schemaVersion`) for scripting; `--name <repo>` labels the report
(omitted, it prints `(unnamed)`, never guessed from the checkout path).

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Connecting a SynapCTX account

Optional, and additive. Nothing about the behaviour above changes.

```bash
sctx init
```

The API key is read from a prompt, or from stdin when piped. It is never accepted
as a command-line argument, where it would be visible to any process able to list
the process table.

One installation can hold a key per organization - run `sctx init` once for each,
and `sctx doctor` will list them. Work in a repository is attributed to that
repository's organization automatically; `sctx init --default` chooses which
organization to credit for work outside any repository.

With an account connected:

- **Savings become visible across a team** in the SynapCTX console, per
  repository and per developer, rather than only in your local ledger.
- **`sctx watch` becomes available**, described below.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## `sctx watch` - uncommitted code your agent can see

An index of a codebase is built from commits, which makes it reliably wrong about
one thing: the code you are changing right now. An agent asking about a function
you refactored ten minutes ago is answered from the version before you touched it.

`sctx watch` closes that gap. It watches your working trees and sends the
*structure* of uncommitted code - symbol names, signatures and doc comments - so
retrieval answers with the version in front of you.

```bash
sctx watch                  # watches ~/git/github.com by default
sctx watch --root ~/work    # or wherever your checkouts live
```

&nbsp;

| | |
| :--- | :--- |
| **Sent** | symbol names, signatures, doc comments, content hashes |
| **Never sent** | function bodies, file contents, anything `.gitignore`d |
| **Who can see it** | only you. It is never shared with teammates, and it expires |
| **How to stop** | stop the command. It runs in the foreground and installs nothing |

&nbsp;

That summary is printed every time, before anything is sent. Results originating
from your working tree are labelled `UNCOMMITTED` in the answer, so they are never
mistaken for code on a branch.

This is the only command that requires an account, because there is nowhere to
send working-tree structure without one. It runs a companion binary, `sctxd`,
included in the Homebrew and release installs.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Configuration

`sctx` resolves settings from the environment first, then
`~/.config/sctx/config.toml` (written by `sctx init`), then built-in defaults.

The file is optional. A missing one is normal, and a malformed one produces a
single warning and is ignored - configuration problems cannot break a command.

&nbsp;

| Variable | Default | Purpose |
| :--- | :--- | :--- |
| `SCT__FORCE_TIER` | *(unset)* | pin the compression level: `aggressive`, `relaxed`, `verbatim`, `off` |
| `SCT__TELEMETRY_ENABLED` | *(unset)* | force sharing on or off, overriding the saved answer |
| `SCT__MAX_OUTPUT_BYTES` | `8388608` | output above this size is buffered to disk rather than memory |
| `SCT__RAW_CACHE_ENABLED` | `false` | retain byte-exact raw output locally when a formatter explicitly omits content |
| `SCT__RAW_CACHE_DIR` | `~/.config/sctx/raw` | owner-only recovery cache directory |
| `SCT__RAW_CACHE_TTL` | `24h` | maximum lifetime of a recovery entry |
| `SCT__RAW_CACHE_MAX_BYTES` | `67108864` | total recovery-cache size limit; oldest entries are removed first |
| `SCT__STATS_DB_PATH` | `~/.config/sctx/stats.db` | local savings ledger |
| `SCT__SPOOL_DIR` | `~/.config/sctx/spool` | queue for usage events awaiting delivery |
| `SCT__TELEMETRY_ENDPOINT` | `http://127.0.0.1:6221/v1/telemetry/exec` | usage-event destination; `sctx init` sets this to `https://sctx.synapctx.com/v1/telemetry/exec` |
| `SCT__WORKSPACE_PROXY_URL` | `http://127.0.0.1:6220` | destination for `sctx watch`; `https://mcp.synapctx.com` for the hosted platform |
| `SCT__WATCH_HELPER` | *(unset)* | path to `sctxd`, if it is not installed alongside `sctx` |
| `SCT__REDACT` | `false` | scrub secrets from output before it reaches your agent - see [Secret redaction](#secret-redaction-opt-in) |

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Privacy

Raw-output recovery is off by default because retaining command output after
the process exits changes the local privacy posture. When explicitly enabled,
sctx writes only genuinely elided runs to an owner-only local directory, prints
the recovery path, expires entries after the configured TTL, and bounds total
disk use. Raw bytes and recovery paths are never added to telemetry.

Without an API key, nothing leaves the machine. The default destination is a
loopback address, and delivery without a key is refused rather than attempted.

With an account, two distinct things may be shared, and consent is asked for them
separately because they are not the same request:

- **Your savings** - command names and token counts, so your own console can
  report what `sctx` saved. Covered by holding an account.
- **Coverage gaps** - the *name alone* of a command no renderer matched, so the
  most-needed renderer is built next. **Opt-in**, because its value comes from
  pooling across users and an account is not agreement to that. `sctx setup` asks
  once, and an empty answer declines.

&nbsp;

Your own savings report also carries which coding agent ran the command (`claude-code`,
`codex`, `gemini-cli`, `cursor`, `copilot-cli`, `droid`, `kilo`, `opencode` or `shell`),
that agent's own opaque session id, whether sctx was bypassed and how, and a
one-way, salted fingerprint of the command line — a 64-bit hash that never
leaves enough information to reconstruct the command, only enough to say "you
ran this again". The salt is generated once per machine and never transmitted.

Paths, filenames, arguments and command output are never transmitted. The queue is
inspectable, unrendered, at any time. If a repository's organization has no key
configured, its events are delivered under your configured default org's key
instead, with the repository name cleared, so another organization never learns
it, and `sctx flush`/`sctx doctor` say so when this happens.

```bash
sctx telemetry             # what is currently shared
sctx telemetry --preview   # the exact queued events, raw
sctx telemetry --disable   # stop sharing, and delete anything queued
```

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Secret redaction (opt-in)

`sctx` can scrub secrets out of a command's output before it reaches your
agent's context: AWS/GitHub/Slack/Stripe/Google/SendGrid/npm keys, PEM private
keys, JWTs, `Authorization: Bearer` headers, `sctx_live_` keys, and a generic
`key|secret|token|password=value` pattern (gated on entropy so
`password=changeme` in a fixture is never flagged). A match is replaced with
`[REDACTED:<rule-name>]` - the marker, not the value, so an accidentally
committed credential in a build log or test failure never reaches the model.

It runs on the FINAL rendered bytes, after every compression tier
(aggressive/relaxed/verbatim) has already produced its output, so a secret
cannot survive because a tier chose not to compress it - and on the raw-output
recovery sidecar too, since that is exactly what an agent reads back on
request.

Off by default this release. Enable it with:

```bash
SCT__REDACT=true sctx <cmd>
# or, persistently:
echo 'redact = "true"' >> ~/.config/sctx/config.toml
```

`sctx doctor` reports whether it is active (`redaction: on` /
`redaction: off (opt in with SCT__REDACT=true)`), and `sctx gain` prints
`secrets kept out of the model context: N` whenever the window's total is
non-zero.

**The plan is to flip the default to on in a future release**, keeping
`SCT__REDACT=false` as the opt-out for anyone who needs byte-exact output.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Building from source

```bash
git clone https://github.com/synapctx/sctx.git
cd sctx
make build     # ./bin/sctx
make test
make install   # ~/.local/bin/sctx
```

Requires Go 1.26 or newer. There are no code-generation steps, no external
services and no private dependencies involved in building or testing.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Adding a renderer

A renderer is a self-contained package, and new tools are the most useful
contribution anyone can make.

Implement `format.Formatter` (see `internal/domain/format/format.go`) in a new
package under `internal/adapters/format/<tool>/`, register it in
`cmd/sctx/main.go`, and add the command to the rewrite table in
`internal/adapters/hook/rewrite.go` so the agent integration picks it up.

Four rules, and they are the whole review:

1. Return `format.ErrTierInapplicable` when a compression level does not apply, so
   the next one is tried.
2. Never emit an empty body for non-empty input.
3. Mark every omission with an explicit `+N`.
4. When the command failed, compress the noise and keep the error.

**Build fixtures by running the real tool, on the platform it will run on.**
Implementations of the same utility differ more than they appear to - GNU and BSD
variants disagree on wording, ordering and diagnostics - and a renderer written
from memory tends to decline on exactly the systems that matter.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Contributing

Issues and pull requests are welcome. Please include tests, and run `make test`
and `make vet` before submitting.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## Security

If you discover a security vulnerability, please report it privately to
[security@synapctx.com](mailto:security@synapctx.com) rather than opening a
public issue.

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

## License

MIT - see [LICENSE.txt](LICENSE.txt).

&nbsp;

🔝 [back to top](#sctx)

&nbsp;

&nbsp;

---

### SynapCTX

[Website](https://synapctx.com) &nbsp;|&nbsp; [sctx](https://synapctx.com/sctx/) &nbsp;|&nbsp; [LinkedIn](https://www.linkedin.com/company/synapctx) &nbsp;|&nbsp; [BlueSky](https://bsky.app/profile/synapctx.com) &nbsp;|&nbsp; [GitHub](https://github.com/synapctx)

<sub>&copy; SynapCTX. All rights reserved.</sub>

&nbsp;
