# helmfile fixtures

Captured with `ghcr.io/helmfile/helmfile:v0.171.0` (linux/arm64) on 2026-11-21
over a `helm create demo` chart with two releases, in a directory mounted at
`/work/proj`. `template.*` exit 0; `lint.*` exit 0; `diff-cluster-unreachable.stderr`
exit 1 (no cluster reachable, so `helm diff` fails: the retained diagnostic).
