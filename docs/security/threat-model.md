# Threat model

Scope: the Phase 0 design (`spo-runtime`, `spo-controller`, Swarm named generic resources, CDI). Re-review is required before Phase 1 privileged features land.

## Assets

- Host devices and the kernel interfaces behind them.
- Host filesystem and processes, reachable through CDI hooks and mounts.
- The binding between a device and its task.
- Node client keys and controller server keys.

## Actors and trust

| Actor | Trust | Notes |
|---|---|---|
| Swarm manager API users (deploy services) | trusted, root-equivalent | can already run privileged containers via plugins or bind mounts |
| Docker API users on any node | trusted, root-equivalent | can forge labels on non-Swarm containers |
| Root on a node, CDI spec authors | trusted | |
| Workload code inside a task | **untrusted** | controls its own env, files, and network traffic |
| Image authors | **untrusted** | control image `ENV` and `LABEL`, which become container env and labels |
| Other hosts on the Swarm or overlay network | **untrusted** | |
| Unprivileged local users | **untrusted** | |

## Trust boundaries

```
[workload]  --env, files-->  [dockerd]  --bundle-->  [spo-runtime (root)]  --mTLS-->  [spo-controller]  --unix-->  [manager dockerd]
untrusted                    trusted                  trusted                          trusted (has docker.sock)
```

## Threats

| ID | Threat | Mitigation | Verification |
|---|---|---|---|
| T1 | Workload forges `DOCKER_RESOURCE_*` to get a device | Env is only a lookup hint; assignments come from the manager task | `TestGateForgedResourceEnvFailsClosed`, `TestGateForgedDuplicateValueIgnored` |
| T2 | Service owner forges `com.docker.swarm.*` labels on a Swarm task | The executor overwrites reserved keys | `TestGateExecutorOverwritesForgedSwarmLabels` |
| T3 | Network attacker impersonates the controller | TLS 1.3, pinned CA, required server name | `internal/allocation` tests |
| T4 | Node A reads node B's allocations | Client certificate CN must equal `task.NodeID` | `internal/controller` tests |
| T5 | Stale or replaced task receives a device | Desired state must be `running`; task version is audited | `internal/controller` tests |
| T6 | CDI spec adds hooks, mounts, `/dev/mem`, or net devices | Policy allowlists, a denylist, and root-owned dirs | `internal/runtime` inject tests |
| T7 | Symlink or traversal in device paths | Clean absolute paths under `/dev`, `Lstat`, major:minor match. Residual race needs root write access to `/dev`. | `internal/runtime` inject tests |
| T8 | Unallocated workload calls `mknod` on a device | The cgroup v2 device filter denies `open` | `TestGateUnallocatedTaskCannotOpenDevice` |
| T9 | Controller outage blocks the node | Only hinted containers call it; others pass through | `TestGateControllerUnavailableFailsClosedAndRecovers` |
| T10 | Controller compromise | It mounts the manager `docker.sock`, so compromise means cluster root. Code is minimal and read-only. **Planned:** a socket proxy that allows only `GET /tasks/{id}` (#16). | open |
| T11 | Docker API user on a worker creates a container labeled with a victim's task ID | Out of scope (root-equivalent). **Planned:** a Phase 2 lease binds an allocation to the first container ID (#15). | open |
| T12 | Same-node overlap after a partition | ADR 0002 L1. **Planned:** a Phase 2 node-local lease (#15). | open |
| T13 | Secrets in logs or metadata | The audit log records IDs, decisions, and device names only. Keys are 0600 root, and controller keys are Swarm secrets. | review |
| T14 | Malformed config disables the node | A malformed config fails `create` only, which is loud. Other subcommands still run, so containers can be stopped. | `internal/runtime` config tests |
| T15 | Untrusted **image** sets `LABEL com.docker.swarm.*` and `ENV DOCKER_RESOURCE_*`, then is started with plain `docker run` to impersonate a running task | The container name must be the executor's `/<service>.<slot\|node>.<taskID>`, which images cannot set. The manager container ID is compared when present. Found by independent review; the original design trusted labels alone. | `TestGateImageForgedTaskLabelsRejected` (fails on the pre-fix code) |

## Explicit non-claims

- The device cgroup controls which device nodes a container may open. It does **not** isolate GPU memory, execution contexts, DMA, or firmware. Exclusive allocation is the isolation boundary, and it is only as strong as the driver.
- SPO does not protect against root, or anyone with Docker API access, on a node.
- Phase 0 certificates come from a test CA (`internal/pki`, 24h validity). Production issuance and rotation are not designed yet (#17).
