# Docker native fixtures

These files were captured verbatim from Docker CLI/Engine `29.6.1`
(`darwin/arm64` client, Docker Desktop Linux `arm64` engine) and Docker
Compose `v5.3.0` on 2026-08-15.

Every container, image, network, and volume created for capture carried
`com.synapctx.sctx.formatter-test=true`; names used the
`sctx-formatter-20260815` prefix. The fixture project was removed after the
closure run. No verbosity or machine-output option was added by sctx.

Suffixes record the native stream (`.stdout` or `.stderr`). Success fixtures
have exit `0`. `build-failure.stderr` and `exec-transport-error.stderr` have
exit `1` and are retained to prove failure diagnostics stay native.

The corpus covers Docker 29 tables, container states, exact-repeat logs,
BuildKit success/failure, pull progress, inspect JSON, stats, history, top,
network/volume lists, Compose ps/up/down/logs, and exec success/transport
failure. Push was not sent to an external registry; pull and push share the
same layer-progress renderer, while push failure behavior is covered by unit
tests and the common non-zero verbatim path.
