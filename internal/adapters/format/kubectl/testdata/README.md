# kubectl native fixtures

These files were captured verbatim with `kubectl v1.36.1` (`darwin/arm64`)
against a development cluster context on 2026-08-15. All resources lived
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

## kind captures (kubectl v1.36.1, kind v1.37 cluster, 2026-11-21)

Captured against a disposable local kind cluster (`kind-sctxcov3`, deleted
after capture), workloads: a 3-replica Deployment, a Service, a
CrashLoopBackOff pod, an ImagePullBackOff pod and a failing Job.

| Fixture | Native command | Exit |
|---|---|---:|
| `describe-pod-crashloop.txt` | `kubectl describe pod crasher` | 0 |
| `describe-pod-imagepull.txt` | `kubectl describe pod badimage` | 0 |
| `describe-node.txt` | `kubectl describe node <node>` | 0 |
| `get-all-namespaces.txt` | `kubectl get all -A` | 0 |
| `config-view-raw.txt` | hand-built kubeconfig (all key material is fake); real captures hold private keys | 0 |

## `get-pods-all-namespaces.txt` (kubectl v1.36.1, kind v1.37 cluster, 2026-11-21)

`kubectl get pods -A` captured against a disposable local 4-node kind cluster
(`kind-sctxfix`, deleted after capture). Workloads: the public cert-manager
v1.16.2 and ingress-nginx (kind provider) manifests, plus nginx, redis,
postgres and busybox Deployments and Jobs spread over eight namespaces. It
holds 266 pods: Running, Completed Job pods, one CrashLoopBackOff, one
ImagePullBackOff, one Pending, and Error pods with recent restarts. Names are
generic and public; never replace this with a capture of a real cluster.
