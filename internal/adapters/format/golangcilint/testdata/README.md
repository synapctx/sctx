# golangci-lint native fixtures

All files were captured on 2026-08-14 with `golangci-lint v2.12.2`, built
with Go 1.26.2. Commands ran in disposable Go modules without an sctx wrapper.

| Fixture | Native command | Exit | Configuration |
|---|---|---:|---|
| `golangci-lint-v2.12.2-default.txt` | `golangci-lint run ./...` | 1 | no repository config |
| `golangci-lint-v2.12.2-clean.txt` | `golangci-lint run` | 0 | no repository config |
| `golangci-lint-v2.12.2-warning-and-issue.txt` | `golangci-lint run --new` outside Git | 1 | no repository config |
| `golangci-lint-v2.12.2-typecheck.txt` | `golangci-lint run` with an undefined symbol | 1 | no repository config |
| `golangci-lint-v2.12.2-config-error.txt` | `golangci-lint run` | 3 | v2 config naming an unknown linter |
| `golangci-lint-v2.12.2-timeout.txt` | `golangci-lint run --timeout 1ns` | 4 | no repository config |
| `golangci-lint-v2.12.2-build-tags.txt` | `golangci-lint run --build-tags fixturetag ./...` | 1 | no repository config |
| `golangci-lint-v2.12.2-same-location.txt` | `golangci-lint run` | 1 | perfsprint + staticcheck, `issues.uniq-by-line: false` |
| `golangci-lint-v1.64.8-default.txt` | `golangci-lint run ./...` | 1 | no repository config |

The original multi-file fixture was captured from:

- command: `golangci-lint run ./...`;
- native exit code: `1` (issues found);
- output mode: native default text, including issued source lines and stats;
- repository configuration: no `.golangci.yml`, `.golangci.yaml`, `.golangci.toml`, or `.golangci.json` was present.

The fixture is intentionally kept as native output. Tests inspect the render;
they do not modify the command to manufacture a larger savings denominator.

Additional native probes verified `--new-from-rev=HEAD`, forced ANSI colour,
the ambient `NO_COLOR=1`, text without source lines/linter names, and JSON on
stdout. Those are represented by focused inspection tests where storing another
near-duplicate fixture would add no independent output grammar.

The v1.64.8 probe used the final v1 release built locally with Go 1.26.5. Its
default finding grammar is compatible, but materially differs from v2: a clean
run is completely silent and the default issue stream has no summary/statistics
footer. Its explicit output selection also uses `--out-format`; all such
requests deliberately pass through verbatim.
