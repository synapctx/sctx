# sctx

Token-optimizing command wrapper for AI coding agents (Go). Runs a developer
command, re-renders its output token-minimally, and accounts the savings.

This file orients a coding agent working in this repository. It is deliberately
short: it states the invariants you must not break, not the history behind them.

## Layout

- **Module root is the REPOSITORY root** — module path `github.com/synapctx/sctx`,
  entry point `cmd/sctx`. This is the one repository in the org that does NOT use
  the `app/` layout, and the reason is not style: for a root module path Go
  resolves packages from the repository root, so with `go.mod` in `app/` the
  module was fetchable but EMPTY at every documented import path. The advertised
  `go install github.com/synapctx/sctx/cmd/sctx@latest` failed with "does not
  contain package", and so did importing `pkg/agentdoc`. Nothing in the repo
  caught it, because a local build never resolves through the proxy. Moving
  `go.mod` back under `app/` reintroduces that silently — `make build` and
  `go test ./...` would both still pass.
- Hexagonal: `internal/domain` (ports: format, exec, stats, telemetry) →
  `internal/application` (run = the wrap-a-command pipeline; report = `gain`) →
  `internal/adapters` (osproc runner, `format/*` formatters, sqlite stats, spool
  telemetry) → `internal/platform` (config, tokenizer, iospill).
- **`pkg/agentdoc` is the ONLY exported package, and it has one consumer.**
  synapctx.com's `/sctx/` page publishes the same setup guidance for people who
  have not installed the binary, so it holds the instruction documents, the
  `KnownAgents` table and the pure block rendering. Everything that touches a
  disk — detection, sidecar writes, the refusal to overwrite an edited file —
  stays in `internal/platform/agentsetup`, because a web page sees no machine and
  must not pretend to. Two rules keep it safe: it must stay **stdlib-only**, or
  the website inherits sqlite and `x/term` by importing it; and
  `Wrap`/`BlockOf` must round-trip, or guidance pasted by hand is not recognised
  as ours and the next `--install` appends a second copy that then loads into
  every session forever. Both are under test
  (`TestAFileWrittenTheWayTheWebsiteDescribesItReportsTaught`).
- **This module has NO private dependencies, and that is a hard requirement.**
  `sctx` is public and free: a stranger must be able to run `go build`, `go vet`,
  `go test`, `go mod tidy`, `go mod download` and `go mod verify` from a plain
  clone. Never add a `replace` pointing at a sibling checkout. `go build`
  succeeding does NOT prove this holds — Go loads modules lazily, so an unused
  private require passes `build` and fails `tidy`. Check all six.

## Build / test

```bash
make build     # bin/sctx
make test      # go test -race ./...
make vet
make install   # ~/.local/bin/sctx
```

## Invariants — do not break these

- **The wrapped command's exit code and output are sacred.** Stats and telemetry
  failures must never affect either. Telemetry never blocks: local spool plus a
  deadline-bounded flush.
- **Tier chain** (`application/run/tierchain.go`) is aggressive → relaxed →
  verbatim. Any tier error, panic or anomaly DEGRADES to the next tier; it never
  suppresses output. Empty renders and non-smaller renders are anomalies.
- **Never compress away error signal on a non-zero exit**, and mark every elision
  explicitly (`+N` more, `×N` repeated). A reader must be able to tell that
  something was dropped.
- **Formatters**: one package per tool under `adapters/format/`, implementing
  `format.Formatter`, registered in `main.go`. `ErrTierInapplicable` means "try
  the next tier" (expected); any other error is an anomaly.
- **EVERY TIER GETS ITS OWN READERS** (`renderChain`). One `format.Input` used to
  be shared across the chain, so the first tier that READ stdout left the next an
  empty stream — and reading before deciding is the normal shape of a formatter.
  Any formatter whose aggressive tier read and then declined therefore had a
  **dead relaxed tier**, reported as the innocuous "declined: no tier handles this
  invocation". Measured cost: `make` 167 runs at 0% saved (101 declining), `ssh`
  declining 171 of 176. Unit tests could not catch it because a test builds a
  fresh Input per tier — the tier passes alone and fails only in composition, so
  the guard (`TestEveryTierGetsItsOwnReaders`) drives `renderChain`, not a
  formatter.
- **A dedicated formatter's DECLINE is not a dead end.** After every tier of the
  command's own formatter declines, `renderChain` offers the bytes to
  `Options.LosslessFallback` (`jsoncompact`, relaxed tier only) before verbatim.
  Without it, `mongosh` printing a JSON document went out at full size while an
  unmatched command printing the identical bytes was compacted. It is
  LOSSLESS-ONLY on purpose: most of the ~338 declines are deliberate — a
  formatter stepping aside so user-selected machine output stays authoritative —
  and nobody has enumerated which mean "not my shape" and which mean "hands
  off", so the fallback must be safe without knowing. Whitespace-compacting a
  JSON document is (same values to every parser); collapsing repeated LINES is
  not, which is why the generic line collapser stays on the unmatched path.
  A fallback render is accounted as `(generic)` with `FormatterKind=generic` —
  it is a saving, not coverage.
- **The generic fallback (`adapters/format/generic`) applies to EVERY unmatched
  command**, not just JSON. It compacts what parses as JSON and collapses runs of
  identical (or identical-after-a-leading-timestamp) lines, sharing
  `adapters/format/collapse` with `read`. It is safe without a per-tool fixture
  precisely because it compresses only **provable redundancy** — a collapsed run
  is reconstructible from the line plus its printed count — so it never assumes a
  shape the way a speculative table parser would. It records itself as
  `(generic)` with `FormatterMatched=false`, because a caught command is not a
  covered one and the gap meter must keep telling them apart.
- **A following command is never wrapped** (`streamsForever`). sctx reads stdout
  to EOF before formatting, so wrapping `tail -f`, `kubectl logs -f` or
  `journalctl -f` turns "the agent sees lines until its timeout" into "the agent
  sees nothing". Scoped to the subcommand that actually streams, never per
  program: `docker build -f Dockerfile`, `helm install -f values.yaml` and
  `make -f Makefile` all name a FILE, and a blanket rule would silently drop their
  coverage.
- **Write a formatter against output captured from the real binary, on the
  platform the workload runs on.** Tool output differs by implementation and
  platform far more than it looks — capture fixtures by running the thing, never
  from memory.
- **A transport/build formatter can DELEGATE a region of its own output to
  another formatter without forking that formatter's parser.** `makefmt`
  (2026-09-04) recognises a bare `go test`/`go vet`/`go build` or
  `golangci-lint run` recipe echo and hands JUST that region's lines to
  `gotest`/`golangci-lint`'s own Aggressive-then-Relaxed tiers, splicing the
  render back into make's own directory/dedupe collapsing
  (`adapters/format/makefmt/delegate.go`). A recipe that pipes or chains the
  tool (`go test ./... | tee log`) declines delegation for that region on
  purpose — the captured stream is no longer the tool's own. `ssh`'s
  delegation (`platform/nestedcmd.Remote`) was widened the same day to accept
  a PIPELINE whose head is a registered program and whose trailing stages are
  all line-narrowing (`head`/`tail`/`cat`/`less`/`more`, mirroring
  `hook.pipeSafeDownstream`) — `ssh host 'go test ./... | tail -20'` now
  delegates to `go test`'s formatter over the (possibly truncated) combined
  output. `sed` (`adapters/format/sed`, row + grammar in
  `platform/sedargv`) and `gofmt` (`adapters/format/gofmt`, delegating `-d`
  to `filediff`) are new leading-program formatters from the same pass: sed
  only for the two read-only shapes `sed -n 'A,Bp' FILE` / `sed -n '/re/p'
  FILE` (everything else stays in `unformattable`, a documented exception —
  see that map's sed comment — because a program can be partially coverable
  without every invocation being one); gofmt only for `-d` (`-l`/`-w`
  already print at most a short line list or nothing).
- **The hook rewrite must never insert text inside a quoted string or a heredoc
  body.** `hook/rewrite.go` scans quote- and escape-aware, and declines outright
  (fail-open, command untouched) on anything it cannot read confidently:
  subshells, command substitution, backgrounding, unterminated quotes.
  `FuzzRewrite` is the regression guard and checks WHERE insertions land, not
  merely that they can be stripped again. Do not weaken the scanner without it
  passing.
- **A wrapped segment may only be followed by line-narrowing pipe stages**
  (`head`, `tail`, `cat`, `less`, `more`). `grep`/`sed`/`awk`/`sort`/`wc`/`jq`
  are excluded on purpose: they could silently misread compressed output, making
  an elided line look absent rather than elided.
- **Tests must never write to the real telemetry spool.** `hook`'s `TestMain`
  redirects it for the whole package; an opt-in guard is one a new test forgets.
- **Token estimate is bytes/4 on purpose** — conservative, so reported savings
  are a floor. Changing it silently rebases every historical figure.

## Telemetry and consent

- **Telemetry is split by PURPOSE.** `PurposeService` (`exec_savings`, the
  customer's own savings report) is authorised by holding an API key.
  `PurposeImprovement` (`coverage_gap`, which ranks what gets built next) is
  opt-in, because its value comes from aggregating across users. `PurposeOf`
  defaults unknown kinds to *improvement*, so a new event kind cannot ship as
  "service" by omission.
- **Consent gates COLLECTION, not just delivery**, and is enforced again at the
  flush boundary — authorisation can change underneath a spool. Unauthorised
  events are dropped, never retained.
- **A flush POSTs at most `chunkMaxEvents` (200, ~256 KiB) events per
  request, each under its OWN timeout, and removes a chunk from the spool
  ONLY once its own request is acknowledged 2xx** (`spool.go`, 2026-09-04). A
  single flush used to POST an org's entire backlog as one request under one
  shared deadline: once a backlog grew large enough that sending it exceeded
  the deadline, the client always saw a context-deadline error and retained
  every line for resend, while the server — having already received and
  processed the full request body — ingested it anyway. The spool never
  shrank and the same events were re-ingested on every retry. **`sctx`
  events carry an `id` (`telemetry.Event.ID`) that is NOT used as the
  Elasticsearch document id anywhere in graph-retrieval-engine's
  `RecordUsage`** until graph-retrieval-engine v0.6.2 (2026-09-04), which now
  sets the bulk `_id` from the event's own `id`, so a re-sent chunk is an
  upsert of the same document rather than a duplicate. Chunking removes the
  client-side cause; the engine's `_id` makes an accidental resend safe. The
  ~3% of duplicates written before v0.6.2 remain in the index. The opportunistic post-command
  path (`Flush`, via `AutoFlush`) sends AT MOST ONE chunk and never loops, so
  it can never block a wrapped command on a large backlog; `sctx
  flush`/`sctx init` (`FlushWithTimeout`) loop chunk by chunk until the spool
  is empty or a chunk fails outright. A chunk rejected 4xx three times in a
  row (a malformed line from an ancient sctx version, most likely) is
  quarantined to `<spoolDir>/rejected.jsonl` rather than retried forever —
  see `maxConsecutiveChunkRejects`.
- **A keyless org's events are RE-ATTRIBUTED to the default org, never
  dropped and never sent under a sibling org's key** (`spool.go` /
  `flushOnce`, 2026-09-04). A repository whose org has no configured key
  (e.g. a personal or third-party clone with no `sctx init` run against it)
  used to sit in the spool forever, growing until `maxSpoolBytes` silently
  dropped it — the developer's own savings on that work never reached any
  console. Instead, once `default_org` names an org that DOES have a key,
  the event is delivered under THAT key with `repositoryName` cleared to
  `""` (a value the proto already accepts, "not attributed to a
  repository") — the id, program key and token counts are untouched, so the
  saving still counts, but org-isolation rule 0009 (an org must never learn
  another org's repository names) holds because the real name never leaves
  the machine under someone else's bearer token. An event whose own org
  already has a key is untouched. `org == ""` (no repo at all) already
  resolved to the default org through `Config.TokenForOrg` before this
  existed; re-attribution only ever fires for a NAMED org. With no default
  org configured either, the event stays pending exactly as before, and
  `FlushResult.NoKeyEvents` (surfaced by `sctx flush` and `sctx doctor`)
  says which org and how many, rather than the backlog looking like an
  unexplained no-op. The purpose gate (service vs. improvement) is applied
  BEFORE grouping, so re-attribution can never become a bypass for consent.
- **The consent prompt is `[y/N]` and an empty answer declines.** It runs only
  from `sctx setup` on a TTY, never on the wrapped path or in the hook.
- **A decision is stored with the disclosure version it was made against**, and a
  decision older than the current version counts as no decision. A test reflects
  over the telemetry event: a new field fails the build until it is disclosed and
  the version bumped.
- **Client/session provenance and the argv fingerprint are `PurposeService`,
  not `PurposeImprovement`.** `internal/platform/agentenv` resolves which
  coding agent is driving the process (env markers, e.g. Claude Code's
  `CLAUDECODE=1`/`CLAUDE_CODE_SESSION_ID`, with an `SCT__CLIENT`/`SCT__SESSION`
  override; unmatched reports `"shell"`, unrecognised reports `"unknown"` —
  never an arbitrary string). A hook and the wrapped command it triggers run in
  SEPARATE processes with no shared environment, so the hook also writes
  `<spoolDir>/sessions/current` (atomic, 0600) as a fallback the run pipeline
  reads only when its own env carries no session id AND the file is ≤2s old —
  old enough to be a previous session's leftover otherwise.
  `telemetry.Event.ArgvHash` is `hex(sha256(config.ArgvSalt + normalizedArgv))[:16]`:
  one-way, salted with a 32-byte secret generated once per machine
  (`config.ArgvSalt`, persisted to `config.toml`, never transmitted), so it
  says "same command as before" without being reversible or comparable across
  machines. It rides on `exec_savings` (service purpose) rather than
  `coverage_gap`, argued in `telemetry.PurposeOf`'s comment: the customer is
  the only party who ever reads it, on their own console.
  **`persistArgvSalt` (config.go, 2026-09-04) REWRITES, never appends.** It
  used to append a fresh `argv_salt` line every time `Load` saw an empty
  salt — which any single malformed line elsewhere in the file used to
  cause, since `loadConfigFile` discarded the WHOLE file (including an
  already-persisted salt) on one bad line. Compounded across every wrapped
  invocation: 43 duplicate `argv_salt` lines within two hours of v0.7.0 on
  one machine. Now: the malformed-line parser skips only that line (warns
  once); `persistArgvSalt` strips any existing `argv_salt` line(s) before
  writing exactly one, atomically (temp file + rename); a file already
  carrying duplicates is deduplicated once, keeping the LAST value. An
  ordinary wrapped run, once one salt is on disk, never touches
  `config.toml` again. `parseConfigLine` also accepts bare TOML
  `true`/`false`/integers now, not only double-quoted strings — `redact =
  true` (unquoted, valid TOML) used to parse as malformed while `redact =
  "true"` worked; the writer emits bare booleans for boolean keys.
- **Never send paths or filenames.** The program key is `program` or
  `program subcommand`, and which one is decided by an explicit allowlist of
  programs whose first argument is genuinely an operation — never by how the
  token looks. A hostname, a directory and a subcommand are indistinguishable by
  shape.

## `sctx setup`

Installs and verifies the agent-side delivery path: whether the coding agents on
this machine have been told `sctx` and SynapCTX exist, and whether OpenAI Codex
has the MCP registrations that make the SynapCTX tools callable. Four rules are
load-bearing.

- **Write ONLY where an agent already left its own configuration.** Detection is
  by existence, nothing is created speculatively, and detection must never key on
  a file we ourselves write.
- **Content lives between markers and is replaced in place.** An opening marker
  with no close counts as NOT installed. Never overwrite content a user edited;
  only repair a missing include.
- **Sidecar documents carry a provenance STAMP** (`agentdoc.Stamp`, first line, a
  short hash of the body, optionally preceded by the sctx version that wrote it).
  It is what makes "ours and untouched" decidable, and without it a correctness
  fix reached no machine that had already installed:
  `Install` refused to touch any existing sidecar because it could not tell
  customised from a-release-behind, and `Inspect` never read sidecars at all, so
  nothing even reported the drift. Now: stale-but-unedited updates on a plain
  `--install`; edited and pre-stamp files are left alone and REPORTED, since
  neither has a remedy `--install` can apply and a nag without one gets muted.
  **The HASH decides and the VERSION only reports** — versions repeat on `dev`
  builds, go backwards on a downgrade, and are absent from anything the website
  renders, so the hash is the last field and a version-less stamp stays fully
  valid. **The website must serve `StampedBody` too**, or every hand-installed
  file is permanently unverifiable.
- **A document the developer includes THEMSELVES is followed to where their
  include points, and managed there.** It used to be skipped entirely, which left
  the most common real configuration — a hand-written `@~/.claude/SCTX.md` naming
  the exact path we would have used — invisible: never inspected, never updated,
  reported `[ok]` while two releases stale. `agentdoc.IncludeTarget` returns the
  path; `resolveInclude` decides whether we may write there. Two bounds, both
  because the path comes from a file we do not own: it must resolve INSIDE the
  home directory, and its parent directory must already exist. Anything else is
  reported as unverifiable, never created — and a second copy beside the
  instruction file, which nothing would load, is still never written.
- **The instruction files must not restate what the MCP tool descriptions say.**
  Both are sent at the start of every session, and ~55-60% of SYNAPCTX.md was
  duplicated there — the customer paying twice for one sentence. The placement
  rule is written in `pkg/agentdoc/templates.go`: constrains one tool → its
  description; qualifies an answer → rendered on the answer; an action → a tool;
  existence, triggers and machine-local facts → here. `developer-mcp-proxy` holds
  the cross-repo guards (a 10-word shingle comparison, and the COMBINED always-on
  token cost) because only the private side can see both budgets.
- **An include is recognised in any path form**, comparing the final path segment
  only, or the same document gets loaded twice for every session.
- **A row may claim only what was VERIFIED against that agent's shipped binary or
  docs.** The 2026-08-18 audit found the Kilo row pointing at `.kilocode/rules/`,
  a path 7.4.22 treats as legacy while loading global instructions from
  `AGENTS.md` in `KILO_CONFIG_DIR ?? ~/.config/kilo` — and detecting only
  `~/.kilocode`, which a current install never creates. Kilo was installed, in
  daily use, and invisible. Unverified capabilities stay at their zero value,
  which reports as "sctx does not do this here" rather than being assumed.
- **"An existing sctx hook is preserved" is not the same claim as "the RIGHT
  sctx hook is preserved" (2026-09-04, `StaleHookReason` in
  `internal/platform/agentsetup/stalehook.go`).** Presence used to be the
  only test everywhere a hook/plugin is detected (Claude/Gemini's
  `hookPresent`/`findHookEntry`, Codex's TOML block, Cursor/Copilot/Droid's
  own JSON files, the Kilo/OpenCode plugin's `SCTX_BINARY`) — an entry
  invoking `sctx hook <subcommand>` counted as installed FOREVER, even when
  the binary it named was a `dev` build or several releases behind the one
  running `setup` now. `--install` on the real, newer binary left the hook
  wired to the old one untouched, because nothing compared the two paths.
  `StaleHookReason(wired, running, runningVersion)` is that comparison:
  stale when the wired binary no longer exists, reports a dev build, or
  reports an older release — NOT merely when it names a different (but
  equally current) path, since two Homebrew installs on separate PATH
  entries are not a fault. `--install` rewrites the entry'S COMMAND IN
  PLACE (never appends a second one) and prints `rewired <agent> hook: <old>
  -> <new>`; `sctx setup` without `--install` reports `[stale]` with the
  same reason. A hook already pointing at the exact binary running now is
  never re-versioned against itself — `samePath` short-circuits before
  `VersionOf` ever runs a subprocess.
- **The instruction document must not promise a hook to an agent that has none.**
  `InstallHooks` wires Claude Code only, yet SCTX.md told all seven agents "a
  PreToolUse hook rewrites covered commands; do not prefix `sctx` yourself" — so
  five of them were instructed never to type the one thing they had to type. The
  symptom is invisible: commands run fine, they are simply never wrapped.
- **MCP registration is per-agent and per-format.** Codex is TOML with ownership
  markers (codexmcp.go); Kilo Code and OpenCode share one JSON `mcp` schema
  (remotemcp.go), verified against the configuration reference embedded in the
  Kilo 7.4.22 binary. JSON cannot carry an ownership comment, so an entry is ours
  when it is remote AND (points at the configured endpoint OR carries an
  `sctx_live_` bearer token). **The token half is load-bearing**: judging by the
  endpoint alone means sctx disowns its own entries the moment the endpoint
  moves, which is exactly when the rewrite is needed. Everything else in the file
  survives byte-for-byte, an unparseable config is never written to, and a
  sibling `.jsonc` naming one of our servers is reported (it wins the deep merge
  and we cannot rewrite it without stripping its comments).
- **Claude Code gets FOUR hooks, and they are four features.** `ClaudeHooks`
  (agentsetup/hooks.go) installs `PreToolUse(Bash)` -> `hook claude` (the rewrite,
  the only one that produces savings), `SessionStart(startup|resume|clear|compact)`
  -> `hook claude-session-start` (the brief: org memory for this repository, index
  freshness vs local HEAD, the tools to open with), `PreToolUse(Grep|Glob|Agent)`
  -> `hook claude-first-search` (twice per session, then silent), and
  `PostToolUse(Edit|Write|Bash)` -> `hook claude-post-tool` (memory for an edited
  file, cross-repo call sites after a grep). Two of them share the `PreToolUse`
  event, so detection and status are keyed on (event, matcher, SUBCOMMAND) —
  keying on the event alone reports one as the other.
  - **SessionStart takes plain stdout, not a hook envelope.** Every other hook
    prints `hookSpecificOutput` JSON; SessionStart injects raw stdout as model
    context, and an envelope printed there is shown to the model verbatim as
    JSON. `writeAdditionalContext` therefore takes the event name — the host
    silently discards an envelope whose `hookEventName` does not match.
  - **`~/.claude.json` is READ, never written**, to name the MCP server for this
    org. The match is on the CREDENTIAL (the entry whose `Authorization` header
    is this org's bearer), not on a name pattern: on the owner's machine the
    servers are `parlitrack` and `cloudresty` with no `synapctx-` prefix, so a
    pattern match would fail exactly where this matters. Unresolved falls back to
    prose, never to a guessed tool name.
  - **The PostToolUse hook now forwards its `session_id` to the proxy (2026-09-04)**, so lookups it fires on every Edit/Write/Bash are attributed instead of arriving with no tool and no session id at all — measured 2026-09-04 at ~30 such unattributable `for-file`/`for-symbol` lookups/minute across six coding agents, inflating a recall-vs-retrieve usage analysis 27x. `memory.go`'s `postToolCall.SessionID` (from the hook's own `session_id` field) is sent as `sessionId` on both surface calls; the proxy stamps `CallMeta{Tool: "post_tool_file"/"post_tool_symbol", Session: ...}` from it (see developer-mcp-proxy's CLAUDE.md) and the engine skips embedding the query entirely for a `for-file` lookup, since the file path is already an authored binding. `claude-first-search` makes NO network call at all (documented on `RunClaudeFirstSearch`) and has nothing to attribute.
  - **Proactive guidance v2 (2026-09-04): novelty-aware offers, a blast-radius nudge on Edit/Write, and an org-level workspace brief.** `postToolBudget` is 2800ms (was 1200), because an Edit/Write now makes up to TWO surface calls — for-file and for-symbol in its new `mode:"edit"` shape — run CONCURRENTLY under that one shared deadline (stdlib `sync.WaitGroup`, no errgroup dependency; each writes to its own result variable so nothing races). `hook/offerstate.go` persists per-session state at `sessions/<id>.offers` (same directory/naming/TTL pattern as `repeatnudge.go`'s own `.repeat` file — a SEPARATE file, unrelated lifecycle): per file, `lastOfferedAt`/`newestStamp`/`pointerShown`; per session, a `fullOffers` count and a symbol→last-asked-at map. The 60s same-file debounce skips the for-file network call entirely; a changed exported symbol already asked about within 10 minutes skips the for-symbol call for that name. `hook/exported.go` computes what to ask about: it diffs `old_string`/`new_string` DECLARATION LINES (`func`/method/`type`) for `.go` files only, keeping names that are new or changed AND exported, capped at 3 — no parse, because an edit fragment is rarely valid standalone Go. `memory.go` renders the response by `mode` (an ABSENT `mode` — an old proxy — is treated as `full`, today's behaviour): `full` is ≤2 notes ≤280 chars each with a `[kind · date · verified|unverified · stale?]` label; `pointer` is one line (`SynapCTX: N notes on <file>, newest <date> — recall_memory`); `silent` renders nothing. The for-symbol edit-mode line names the OTHER repositories, the reference count and the exact `find_references` call — rendered in THIS MACHINE'S own tool namespace via `claudeServerNameFor`/`toolName`, not whatever name the proxy guessed, for the same reason the session brief does. `sessionstart.go`'s `runClaudeWorkspaceBrief` handles the one case `RootAndName` cannot: a workspace root that is not itself a repository but holds several checkouts side by side. `gitrepo.ChildRepos` scans immediate child directories only (cap 200 entries, ≤100ms wall), ranks by `.git/index` mtime, and the hook keeps the busiest 8, groups them by organization (the origin URL's own org) and asks `/v1/surface/for-workspace` for whichever organization holds the most — rendered to ≤1,200 tokens (bytes/4, the same conservative estimate as everywhere else in sctx) with a header, one line per repository, ≤4 org notes, the retrieval hint and a tools line. A 404 from an older proxy that predates the endpoint is silence, like every other failure in these hooks.
  - **The first-search counter lives in `<spoolDir>/sessions/<session_id>`**, the
    same spool the Bash hook uses (`spoolDir()`, shared). Session ids are
    client-supplied and become filenames, so they are sanitised to
    `[A-Za-z0-9._-]` and a name of only dots is refused. Read-modify-write with
    no lock is deliberate: one developer, one agent, and a lost update costs one
    extra nudge.
  - **The PostToolUse Bash branch also nudges on a repeated IDENTICAL run
    (2026-09-04, `internal/adapters/hook/repeatnudge.go`)**: if the same
    normalized argv has produced the exact same raw output size at least 3
    times in this session (two indexed queries against the local stats.db,
    `stats.Store.LatestRawBytes`/`IdenticalRunCount`, joined by `session_id`),
    one line asks the agent to stop re-running it. Local-only — no network,
    no API key needed, unlike the memory/symbol nudges this shares a hook
    process with — so `runClaudePostToolBash` computes both independently and
    joins non-empty results into one `additionalContext` body. Rate-limited
    per session, state at `<spoolDir>/sessions/<id>.repeat` (a SEPARATE file
    from the first-search counter — unrelated lifecycles, would race on one
    file): at most once per (session, argv), at most 3 nudges total. An
    allowlist (`repeatNudgeAllowlist`) exempts commands that legitimately
    repeat — `git status`/`log`/`diff`, `kubectl get`, `ls`, `cat`, `head`,
    `tail` — keyed on (program, subcommand) after stripping the rewrite
    hook's own leading `sctx` token.
- **Auto-wrap is per-client and there are three mechanisms.** A hook process in
  JSON settings (Claude Code, Gemini CLI), a hook in TOML with a trust step
  (Codex), and an in-process plugin (Kilo Code, OpenCode) — all reporting through
  one `WrapState`, because the question a customer has is "are my commands being
  wrapped", not "which mechanism". Windsurf and Crush expose no interception
  point and are reported `[manual]`, never `[ok]`.
  - The plugin is a plain file in the agent's own `plugin/` directory: verified
    on Kilo 7.4.22 that a module there loads with NO config entry and NO package
    install. It calls `sctx hook rewrite`, so the rules stay in the binary and an
    older plugin still makes current decisions.
  - **Codex will not run a hook until a human trusts it** (`/hooks`), and trust is
    keyed to the hook definition's hash. Setup says so on the same line it
    reports the hook, because an untrusted hook is silently skipped.
  - Gemini's `BeforeTool` returns `hookSpecificOutput.tool_input`, which MERGES
    over the model's arguments — send only `command`. Codex's PreToolUse contract
    is byte-identical to Claude's `updatedInput`, but keeps its own subcommand so
    hook detection can tell the two installs apart.
  - **Three more agents, verified from primary sources on 2026-09-04, each keep
    the hook in their OWN config file rather than the settings.json map
    `hooks.go` manages (`cursorhooks.go`/`copilothooks.go`/`droidhooks.go`;
    `jsonhooks.go` holds the small JSON read/write both share).** Cursor's
    `preToolUse` hook (matcher `"Shell"`, `~/.cursor/hooks.json`, Cursor 1.7+)
    answers with Cursor's OWN envelope — `updated_input` under
    `permission: "allow"`, never `hookSpecificOutput` — and Cursor requires
    JSON on every code path, so a miss prints `{}`, not nothing. GitHub
    Copilot CLI's `PreToolUse` hook (`~/.copilot/hooks/*.json`, 1.0.73+) has
    two DISAGREEING sources: GitHub's own docs document a camelCase
    `toolName`/`toolArgs` payload answered with `modifiedArgs`, while the rtk
    reference implementation records a LIVE verification that the CLI's shell
    tool is reported under the Claude-shaped `tool_name`/`tool_input.command`
    schema and answers to `updatedInput` — `sctx hook copilot` follows the
    live-verified path and decodes the documented shape too, for anything
    still registering it. Factory Droid's `PreToolUse` hook (matcher
    `"Execute"`, `~/.factory/hooks.json`) answers with Claude's exact envelope,
    but additionally reads Droid's own `commandDenylist`/`commandBlocklist`
    across every settings scope and steps aside on a match — rewriting first
    would dodge Droid's own pattern matching on the command as written.
- **The default MCP host is the hosted one, in code** (`config.DefaultWorkspaceProxy`).
  It was the local dev proxy, and `sctx init` never wrote a host, so every
  customer registered their agents against a port on their own laptop. Nothing
  persists the default into config.toml: the endpoint may move, and a machine
  that never chose a host must follow the binary rather than a value frozen at
  install time. An operator's own host is preserved through every rewrite.
- **"Registered" must mean reachable.** `sctx init` never persisted an MCP host,
  so config.Load fell back to the local dev proxy and every authenticated machine
  registered its agents against `http://127.0.0.1:6220` — reported `[ok]`, with
  every tool call failing to connect. init now writes `workspace_proxy_url` for a
  hosted install, every config rewrite threads an operator's own choice through
  (writeConfigFile rewrites wholesale, so an untracked value is erased), and
  setup probes the host and fails when nothing answers. Any HTTP status counts as reachable: a 401 to an
  unauthenticated probe is the correct answer from a healthy server.
- **Codex instructions and Codex MCP ability are separate setup states.** A
  current `~/.codex/AGENTS.md` with an empty `codex mcp list` is broken, never
  green. SynapCTX registrations live between owned markers in
  `~/.codex/config.toml`; preserve everything outside, update the owned block on
  endpoint/key/org changes, keep the file `0600`, never print a token, and refuse
  a same-named unmanaged table rather than overwriting or duplicating it. Only
  write this block when Codex was actually detected and at least one org key is
  configured.
- **A hook COMMAND STRING is not argv.** `os.Executable()` on Windows routinely
  returns a path with a space (`C:\Users\Jane Doe\...\sctx.exe`), and every hook
  entry this package writes for Claude, Gemini, Cursor, Copilot, Droid and
  Codex is one shell-visible string, not an argv array — so
  `agentsetup.quoteBinaryForCommand` double-quotes the binary path whenever it
  contains whitespace before it is embedded (`hooks.go`, and the per-agent
  `cursorhooks.go`/`copilothooks.go`/`droidhooks.go`/`codexhooks.go`), leaving
  it byte-for-byte unchanged otherwise. Detection has to undo exactly that:
  `splitCommandTokens` (not `strings.Fields`) tokenizes a quoted run as one
  token before `invokesSctxHook`/`hookProgramToken` compare it, and
  `wiredBinary` (`plugin.go`, shared by the JSON/TOML inspectors) strips one
  surrounding pair of quotes off whatever it extracts — both paths feed
  `os.Stat` or an `exec.Command`, neither of which goes through a shell, so a
  quoted string handed to either would report a working install as stale. The
  Kilo/OpenCode plugin needs none of this: `SCTX_BINARY` is a plain JS string
  passed straight to `execFile` (argv, not a shell), so `jsString`'s existing
  backslash escaping is already sufficient.
- **`config.BaseDir()` is the one place `~/.config/sctx` is joined.** `Load`
  and the hook's own spool resolution (`internal/adapters/hook/firstsearch.go`
  `spoolDir`) used to each hand-join it; both now call `config.BaseDir()`,
  which resolves to `%USERPROFILE%\.config\sctx` on Windows via
  `os.UserHomeDir()` + `filepath.Join` — no separate Windows convention (e.g.
  `%LOCALAPPDATA%`), matching where the coding agents themselves already keep
  their configuration on that platform (`~\.claude`, `~\.codex`, `~\.gemini`).

## `sctx watch`

Streams the structural diff of UNCOMMITTED code so an agent's `retrieve_context`
answers about the code being changed rather than the last commit.

- It **requires a SynapCTX API key**. Everything else in `sctx` works without an
  account; this is the only command that sends code anywhere.
- It runs the `sctxd` helper binary as a **child process**, found beside the
  `sctx` binary first, then on `PATH`, with `SCT__WATCH_HELPER` overriding both.
  An override that does not exist is an error, never a silent fallback.
- **Configuration crosses on stdin as JSON, never argv** — argv is visible in
  `ps`. `sctx` resolves config and tells the helper the answers; the helper never
  re-reads `config.toml`.
- **Foreground, and running it is the consent.** `sctx setup` installs nothing
  and nothing auto-starts. The privacy posture is printed before anything is
  sent, and that printing lives here in the public binary so the claim can be
  audited against source.

## `internal/platform/redact` (opt-in this release)

Scrubs secrets from the FINAL rendered bytes of a wrapped command, on every
tier including verbatim — a secret must not survive because a tier chose not
to compress it.

- **Wired into `run.Service.Execute`, gated by `Options.Redact` /
  `config.Config.Redact` (`redact` in config.toml, `SCT__REDACT` env — env
  always wins, default false this release).** Applied AFTER `renderChain`
  returns, never before: redacting raw bytes before a tier reads them could
  perturb that tier's own parsing (e.g. a JSON formatter), so every tier is
  covered by construction rather than by teaching each one about secrets.
  Both live streams get it (the final stdout body, and raw stderr unless
  `FoldStderr` suppressed it), and so does the raw-cache recovery sidecar —
  it is what an agent reads back on request, so an unredacted copy on disk
  would defeat the whole point the moment it is recovered. `Report.Unscanned`
  is surfaced as `[REDACTION-LIMIT: N bytes unscanned]` on stderr.
- **The exit code never passes through redaction.** `Execute` returns it as a
  plain `int`, untouched by anything in the redaction path; guarded by
  `TestExitCodeAndCountsAreNeverRedacted`-shaped tests alongside a proof that
  sctx's own accounting notation (`FAIL ×3`, `…+12 more`) survives redaction
  byte-identical — it must never be mistaken for a secret.
- **`domexec.Command.Tee`** (the live-stream path for unknown commands whose
  progress is followed as it runs) is DEFINED but **not yet set anywhere in
  `Execute`** — no caller constructs a `Command` with `Tee` populated today.
  `redact.NewWriter` is the intended wrapper for that path once it lands
  (`Close` before reading `Report()`, exactly like the buffered case); until
  then it is exercised directly against the real `osproc` runner
  (`TestExecuteRedactionStreamSplitToken`) to prove the mechanism reassembles
  a secret split across two `Write` calls, rather than at the `Service`
  level where there is nothing yet to wire.
- **`stats.Run.RedactedCount` / `telemetry.Event.RedactedCount`** are filled by
  `Service.account`; `stats.Report.RedactedCount` sums them for the scoped
  window and `sctx gain` prints `secrets kept out of the model context: N`
  when it is non-zero (omitted at zero, so a plain install's report is
  unchanged). `sctx doctor` prints the on/off state and the opt-in env var.

- `Rules()` returns a fixed, `sync.Once`-compiled set of regexes (AWS/GitHub/
  Slack/Stripe/Google/SendGrid/npm/PEM private key/JWT/bearer-header/
  `sctx_live_`, plus a generic `key|secret|token|password=value` catch-all).
  The generic rule additionally requires the captured value clear a 3.5
  bits/char Shannon-entropy bar and not be one of a fixed placeholder deny
  list (`changeme`, `<redacted>`, `xxxxxxxx`, `example`, `placeholder`, any
  `your-...`), so `password=changeme` in a fixture is never reported as a
  leak.
- `Apply(b []byte) ([]byte, Report)` replaces each match with
  `[REDACTED:<rule-name>]`. Overlapping matches resolve leftmost-longest, so
  a JWT found inside `Authorization: Bearer ...` yields one marker, not two.
  Only the first 8 MiB (`maxScan`) of `b` is scanned; the rest passes through
  unchanged and its length is surfaced as `Report.Unscanned` so a caller can
  print a notice — it is never silently left unscanned.
- `NewWriter(w io.Writer) *Writer` is the streaming counterpart for the tee
  path: it holds back the last 4 KiB of unflushed data on every `Write` so a
  secret split across two writes is reassembled before `Apply` sees it.
  `Report()` is only complete once `Close` has run.
- Cost is one `FindAll` pass per rule over the buffer (currently 12 rules), not
  a single combined-alternation scan: `BenchmarkApplyOneMiB` measured
  ~7 MB/s (~145ms/MiB) on a mixed log fixture with a few real secrets
  sprinkled in — acceptable for CLI-sized output, worth revisiting with a
  single compiled alternation if it is ever applied to multi-MiB streams by
  default.
