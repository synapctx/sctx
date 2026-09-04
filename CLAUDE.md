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
- **The consent prompt is `[y/N]` and an empty answer declines.** It runs only
  from `sctx setup` on a TTY, never on the wrapped path or in the hook.
- **A decision is stored with the disclosure version it was made against**, and a
  decision older than the current version counts as no decision. A test reflects
  over the telemetry event: a new field fails the build until it is disclosed and
  the version bumped.
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
  - **The first-search counter lives in `<spoolDir>/sessions/<session_id>`**, the
    same spool the Bash hook uses (`spoolDir()`, shared). Session ids are
    client-supplied and become filenames, so they are sanitised to
    `[A-Za-z0-9._-]` and a name of only dots is refused. Read-modify-write with
    no lock is deliberate: one developer, one agent, and a lost update costs one
    extra nudge.
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
to compress it. Not yet wired into the run pipeline; this is the standalone
package only.

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
