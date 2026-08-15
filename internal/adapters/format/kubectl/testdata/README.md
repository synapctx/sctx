# kubectl native fixtures

These files were captured verbatim with `kubectl v1.36.1` (`darwin/arm64`)
against context `plt-euc3-de1-dev-svc-01` on 2026-08-15. All resources lived
in the disposable, labelled namespace `sctx-formatter-20260815-a`; the
namespace was deleted after capture.

No verbosity or output-format flag was injected by sctx. Each fixture records
the native command shape in its filename/content and exists to keep inspection
tests tied to output the real CLI and API server produced.

| Fixture | Native command | Exit |
|---|---|---:|
| `get-pods.txt` | `kubectl -n <fixture-ns> get pods` | 0 |
| `get-multiple-types.txt` | `kubectl -n <fixture-ns> get deployments,services` | 0 |
| `logs-repeated.txt` | `kubectl -n <fixture-ns> logs <repeater-pod>` | 0 |
| `events-warning.txt` | `kubectl -n <fixture-ns> events --types=Warning` | 0 |

Names, ages, restart counts, and event times are intentionally frozen capture
data. Tests must inspect structural invariants rather than current cluster
state.
