# ADR 0002: Swarm's scheduler is the allocator of record; the controller is a stateless resolver

- Status: Proposed. Pending the Phase 0 gate decision in #1.
- Date: 2026-11-18
- Issues: #1, #4, #6

## Context

Exclusive devices must never be bound to two concurrent workloads, even across concurrent updates, duplicated events, controller restarts, and leader changes. The handoff suggests a durable SPO allocation record with generations and fencing.

An external controller cannot join Swarm's Raft log. A separate SPO store would race the Swarm scheduler: Swarm places a task before SPO could reserve for it.

## Decision

1. **Swarm's scheduler is the allocator of record.** Named generic resources (ADR 0001, F5) are bound per task inside the manager's Raft store. The scheduler serializes binding, and a task stays `Pending` when no ID is free (F7). The feasibility suite checks this under concurrent services and scaling (`TestGateExclusiveAllocationAndReclaim`, `TestGateNativeLifecycle`).
2. **`spo-controller` holds no allocation state.** It answers `GET /v1/tasks/{id}/allocation` by reading the task from the local manager. Replicas need no leader election, and a restart loses nothing.
3. **Fencing at injection time:**
   - The caller's certificate CN must equal `task.NodeID`.
   - `task.DesiredState` must be `running`. A replaced or removed task gets nothing.
   - The runtime checks that the returned task, service, and node IDs equal the container's daemon labels.
   - `task.Version.Index` is returned as the generation and recorded in the audit log.
4. **Release** happens when Swarm marks the task terminal. In testing, Swarm started the successor only after the holder's container `die` event (`TestGateExclusiveAllocationAndReclaim`).

## Limitations

1. **L1. Observed ordering, not a proven invariant.** Release-after-die is observed on one node. SwarmKit's guarantee under node partition is not verified. A partitioned node may keep the old container running while the manager binds the ID to a new task on that same node after it rejoins. Mitigation is planned for Phase 2 (#15): a node-local lease keyed by device ID that refuses injection while the previous holder container is still running.
2. **L2. Exclusive only.** Named generic resources model exclusive, count-based binding. Shareable, partitioned (MIG), and capacity-based classes need more design. Sharing must not be inferred from device-node access.
3. **L3. Static inventory.** Advertised IDs change only on a dockerd restart. A device that vanishes stays advertised: the task is placed, injection fails closed, and Swarm retries per restart policy. The agent must surface this (`spo doctor`, metrics) and operators should set restart delays.
4. **L4. Manager API dependency.** If no manager answers, SPO tasks fail closed and recover once it returns (`TestGateControllerUnavailableFailsClosedAndRecovers`).

## Alternatives considered

- **Controller-owned allocation store with leases.** It duplicates Swarm's scheduler and cannot be atomic with placement without upstream changes. It would be needed only for classes Swarm cannot express (L2). Revisit then.
- **Node-label placement plus runtime-side allocation.** It cannot stop Swarm from placing more tasks than there are devices, which causes create failures and restart loops.
