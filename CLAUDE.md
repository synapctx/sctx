# sctx

Public, free Go CLI that runs a developer command, re-renders its output token-minimally and accounts the
savings; also installs agent hooks/instructions (`sctx setup`) and streams uncommitted symbols (`sctx watch`
via the separate private `sctxd` helper). Talks to the hosted SynapCTX proxy for telemetry and memory hooks.

## Build / run / test
- `make build` (bin/sctx), `make test` (`go test -race ./...`), `make vet`, `make fmt`, `make lint`, `make install` (~/.local/bin).
- `make install-sctxd` needs the private sibling `../workspace-delta-daemon/app` (`SCTXD_SRC` overrides).
- Module root is the REPOSITORY root (no `app/`); do not move `go.mod`.
- Public buildability: from a plain clone `go build`, `go vet`, `go test`, `go mod tidy`, `go mod download`, `go mod verify` must all pass.

## Layout
- `cmd/sctx` - entry point and subcommands (`setup`, `watch`, `doctor`, `filters`, `bench`, telemetry)
- `internal/domain` - ports (format, exec, stats, telemetry)
- `internal/application/run` - wrap pipeline and tier chain (`tierchain.go`); `application/report` - `gain`
- `internal/adapters/format/*` - one formatter package per tool; `generic`, `jsoncompact`, `collapse` are fallbacks
- `internal/adapters/hook` - Claude/Gemini/Codex/Cursor/Copilot/Droid hooks, rewrite scanner, nudges
- `internal/adapters/{exec,stats,telemetry/spool}` - osproc runner, sqlite stats, spool telemetry
- `internal/platform/agentsetup` - `sctx setup` install/inspect; `platform/config` - config, consent
- `internal/platform/{redact,rawcache,iospill,tokenizer,agentenv,*argv}` - redaction, raw cache, token estimate, argv grammars
- `pkg/agentdoc` - the ONLY exported package (stdlib-only; shared with synapctx.com)

## External systems
- Hosted SynapCTX MCP/proxy (`workspace_proxy_url`, default in `config.DefaultWorkspaceProxy`) and telemetry endpoint.
- Config at `~/.config/sctx/config.toml`, spool under `<base>/spool`; env prefix `SCT__`.
- `sctxd` helper (private repo workspace-delta-daemon) shipped beside `sctx` in the release archive.
- CI: `.github/workflows/ci.yaml`, `release.yaml`; Homebrew tap `synapctx/homebrew-tap`.

## Invariants
- The wrapped command's exit code and output are exact; stats/telemetry failures never affect either.
- Tier chain is aggressive -> relaxed -> verbatim; errors/anomalies degrade, never suppress output.
- Never drop error signal on non-zero exit; mark every elision (`+N more`, `xN`).
- Each tier gets its own readers (`TestEveryTierGetsItsOwnReaders`).
- LosslessFallback is lossless-only (JSON compaction), never line collapsing.
- Never wrap following/streaming commands (`streamsForever`), scoped to the streaming subcommand.
- Hook rewrite never edits inside quotes or heredocs; declines on anything unreadable (`FuzzRewrite`).
- Only head/tail/cat/less/more may follow a wrapped segment; never grep/sed/awk/sort/wc/jq.
- Capture formatter fixtures from the real binary on the target platform.
- Never add a private dependency or a `replace` to a sibling checkout.
- Tests must not write to the real telemetry spool.
- Telemetry: unknown kinds default to improvement purpose; consent gates collection; never send paths or filenames.
- `pkg/agentdoc` stays stdlib-only; `Wrap`/`BlockOf` must round-trip.
- Setup writes only where an agent already left config, never overwrites edited content, rewrites stale hooks in place.
- Token estimate is bytes/4; do not change it.

## Knowledge in SynapCTX
Decisions, pitfalls and history for this repo live in SynapCTX org memory (synapctx). Use `recall_memory` with your task. File-specific traps surface automatically when you edit those files.
