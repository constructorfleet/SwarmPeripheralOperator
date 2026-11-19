# ADR 0001: Inject devices with an OCI runtime wrapper keyed on Swarm generic resources

- Status: Proposed. Pending the Phase 0 gate decision in #1.
- Date: 2026-11-18
- Issues: #1, #4, #5

## Context

Swarm services cannot express device access. Tested on Engine 29.10 (`docker service create --help`):

| Capability | `docker run` | `docker service create` |
|---|---|---|
| `--device`, `--device-cgroup-rule` | yes | no |
| `--gpus` / CDI device requests | yes | no |
| `--annotation` (OCI annotations) | yes | no |
| `--runtime` | yes | no |
| `--privileged` | yes | no |
| `--generic-resource` | no | yes |

SPO must inject devices into genuine Swarm tasks, before the workload starts, without privilege.

## Observed facts

Phase 0 experiments and the feasibility suite (`tests/integration`) established these. The suite re-checks each one on every run.

1. **F1.** Swarm task containers run through the daemon `default-runtime`. dockerd invokes it through `containerd-shim-runc-v2` as `<runtime> --root … --log … create --bundle <dir> <container-id>`.
2. **F2.** The bundle `config.json` for a Swarm task has no annotations and no labels. `process.env` holds service env, which the service owner controls.
3. **F3.** `GET /containers/{id}/json` blocks while the runtime runs `create`, because dockerd holds the container lock. `GET /containers/json?filters={"id":[…]}` returns at once, with labels.
4. **F4.** The Swarm executor sets `com.docker.swarm.{task.id,service.id,node.id}` and overwrites user values for those keys on its own tasks (`TestGateExecutorOverwritesForgedSwarmLabels`).
   - Image `LABEL`s still become container labels on **non-Swarm** containers, so an image can carry another task's labels.
   - Docker container names cannot come from images. The executor names task containers `<service>.<slot|node>.<taskID>`.
5. **F5.** Named generic resources (`node-generic-resources: ["SPO_RNG=rng0"]`) make the Swarm scheduler bind specific resource IDs to tasks. The binding is stored in `task.GenericResources` and served only by manager nodes.
6. **F6.** Swarm also exports the binding as `DOCKER_RESOURCE_<KIND>=<id>`, appended after user env. A service can add its own `DOCKER_RESOURCE_<KIND>`, so the container ends up with both values. Env cannot identify the assigned device.
7. **F7.** When no ID is free, Swarm leaves the task `Pending` with `insufficient resources`.
8. **F8.** With cgroup v2, a container can `mknod` any char device, but `open` is denied unless the device is in the OCI `linux.resources.devices` allowlist.

## Options

### 1. Native Swarm support (patch SwarmKit/moby)

Add device requests or CDI names to `ContainerSpec` and propagate them through the executor.

- Pro: no wrapper, and the cleanest UX.
- Con: requires upstream acceptance across SwarmKit, the moby API, the CLI, and Compose. A private fork means maintaining a Docker distribution.
- Decision: not selected. Worth pursuing upstream in parallel. SPO's resource model (named generic resources mapped to CDI names) would carry over.

### 2. OCI runtime wrapper (selected)

`spo-runtime` is the daemon default runtime and execs runc after editing the bundle on `create`.

- Pro: edits happen before `runc create`, so before any workload process exists (F1). Works on stock Engine. Uses CDI for device edits.
- Con: wraps every container on the node, so the passthrough path must be minimal. Changing `default-runtime` or `node-generic-resources` needs a dockerd restart. Swarm mode does not support live-restore.

### 3. Post-start permission daemon (`swarm-device-access` style)

A privileged daemon watches container start events and edits device cgroup rules afterwards.

- Con: access is granted after the workload starts (fails G3). The daemon must rewrite other containers' cgroup v2 eBPF programs. And it has no trusted allocation identity.
- Decision: rejected. It is not offered as a fallback, because a silent downgrade would violate the security model.

## Decision

Use option 2 with these rules:

1. Operators advertise each device as a named generic resource on its node: kind `SPO_<CLASS>`, value = stable device ID.
2. Services request `--generic-resource SPO_<CLASS>=N`. Swarm performs placement and exclusive binding (F5, F7).
3. On `create`, `spo-runtime` treats `DOCKER_RESOURCE_SPO_*` env only as a hint that a lookup is needed:
   - No hint: exec runc untouched. Swarm always adds the variable when it binds a resource, so no hint means no binding (F6).
   - Hint present: resolve container ID → task ID from daemon labels via the list endpoint (F3, F4). Require the executor's container name form ending in `.<taskID>`, and the manager-reported container ID when present. Fetch `task.GenericResources` from `spo-controller` over mTLS (F5). Inject only those devices, via CDI, after policy checks.
   - Any failure in the hinted path fails `create`. The error reaches `docker service ps`.
4. Device access comes from CDI `deviceNodes` → OCI `linux.devices` plus a cgroup allow rule (F8). No privilege, no `/dev` bind mounts.

## Consequences

- Every node needs `default-runtime: spo`, plus a dockerd restart to adopt it.
- Inventory changes (hotplug) need a dockerd restart to update `node-generic-resources`. Tracked in #11.
- The controller is on the critical path only for SPO tasks. Other tasks never contact it.
- Labels alone are not trusted. Labels plus a Swarm-form name can be forged only by Docker API users, who are already root-equivalent. See the threat model, T2 and T15.
- `docker run --runtime=runc` bypasses SPO, but also gets no SPO devices.
