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

## `docker run` / `docker compose run` captures (Docker 29.9.3, Compose v5.7.1, 2026-11-21)

Captured against `golang:1.23-alpine` mounting a two-test module
(`TestAddBroken` fails on purpose). Names in the compose stderr are the
throwaway project's.

| Fixture | Native command | Exit |
|---|---|---:|
| `run-go-test-fail.stdout` | `docker run --rm -v <mod>:/w -w /w golang:1.23-alpine go test ./...` | 1 |
| `run-go-version.stdout` | `docker run --rm golang:1.23-alpine go version` | 0 |
| `run-noimage.stderr` | `docker run --rm registry.invalid/nope:1 go version` | 125 |
| `run-nonexistent-cmd.stderr` | `docker run --rm golang:1.23-alpine nosuchbinary` | 127 |
| `compose-run-go-test.{stdout,stderr}` | `docker compose run --rm -T tool go test ./...` | 1 |

## `images-many.stdout`

A synthetic `docker images` listing in Docker 29 column layout, built only
from public images (alpine, busybox, golang, node, postgres, redis, nginx,
and others), with several tags per repository, digest-only references and
dangling `<none>:<none>` rows. Column widths follow a real Docker 29.9.3
capture; IDs and digests for the added rows are made up. Never replace this
with the output of a developer's real image list.
