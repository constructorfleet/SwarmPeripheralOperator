# Allocation protocol

## Steps

| Step | Actor | Mechanism | Durable state |
|---|---|---|---|
| Advertise | operator / `spo-agent` | `node-generic-resources: ["SPO_<CLASS>=<stable-id>"]` | dockerd config |
| Request | service owner | `--generic-resource SPO_<CLASS>=<n>` (Compose: `deploy.resources.reservations.generic_resources`) | service spec (Raft) |
| Bind | Swarm scheduler | picks free IDs on an eligible node, or leaves the task `Pending` | `task.GenericResources` (Raft) |
| Resolve | `spo-controller` | `GET /tasks/{id}` on the manager | none |
| Inject | `spo-runtime` | CDI edits on `create` | audit log |
| Release | Swarm | the task reaches a terminal state | Raft |

## Controller API v1

`GET /v1/tasks/{taskID}/allocation`. Requires a TLS 1.3 client certificate whose CN is the caller's Swarm node ID.

| Status | `code` | Meaning | Runtime action |
|---|---|---|---|
| 200 | | `{taskId, serviceId, nodeId, taskVersion, assignments:[{kind,id}]}`, SPO kinds only | verify and inject |
| 401 | `forbidden` | no client certificate | fail |
| 403 | `forbidden` | task is not on the caller's node | fail |
| 404 | `not_found` | unknown task | fail |
| 409 | `not_runnable` | desired state is not `running` | fail |
| 503 | `unavailable` | manager API error | retry until `lookupTimeout`, then fail |

`GET /healthz` returns 200 and also requires a client certificate.

## Invariants

1. **I1.** An exclusive device ID is bound to at most one non-terminal task. Swarm enforces this, and it is tested.
2. **I2.** A container gets only devices from its own task's binding. The runtime enforces this, and it is tested with forged env.
3. **I3.** A task that is not desired-running gets nothing. Enforced by the controller and unit tested.
4. **I4.** Without a binding there is no device access, even after `mknod`. The cgroup enforces this, and it is tested.

## Failure handling

| Event | Behavior | Evidence |
|---|---|---|
| Controller down | SPO tasks fail `create`; other tasks unaffected; recovery when it returns | `TestGateControllerUnavailableFailsClosedAndRecovers` |
| Duplicate Docker events | not consumed; the protocol is pull-based at `create` | by design |
| Controller restart / failover | stateless; any replica answers | by design; not yet tested with multiple managers |
| Rolling update, rollback, scale | new task, new binding, old one released | `TestGateNativeLifecycle` |
| Holder removed | successor starts after holder `die` | `TestGateExclusiveAllocationAndReclaim` |
| Node down | Swarm reschedules onto another node's devices; IDs are node-local | Swarm behavior; not yet tested |
| Node partition and rejoin | possible overlap on the same node (ADR 0002 L1) | gap: Phase 2 lease |
| Device unplugged | still advertised; injection fails closed (CDI `Lstat`); Swarm retries | unit tested; hotplug untested |
| Stale CDI spec (major:minor changed) | rejected by policy | unit tested |
