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

Installs the agent-side half: whether the coding agents on this machine have been
told `sctx` and SynapCTX exist. Three rules are load-bearing.

- **Write ONLY where an agent already left its own configuration.** Detection is
  by existence, nothing is created speculatively, and detection must never key on
  a file we ourselves write.
- **Content lives between markers and is replaced in place.** An opening marker
  with no close counts as NOT installed. Never overwrite content a user edited;
  only repair a missing include.
- **An include is recognised in any path form**, comparing the final path segment
  only, or the same document gets loaded twice for every session.

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
