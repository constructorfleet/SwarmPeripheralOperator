# Roadmap

Each phase starts only after the previous exit gate is recorded as passed in its gate issue.

## Phase 0: Feasibility (current) — #1

- [x] Engine/Swarm capability research (ADR 0001 facts F1-F8)
- [x] Runtime wrapper PoC (#5), controller PoC (#6)
- [x] Two-node Swarm feasibility suite (#7)
- [ ] Compatibility matrix across Engine versions (#3)
- [ ] Hardware-in-the-loop: NVIDIA GPU and USB (#8). **Blocked: hardware.**
- [ ] Gate decision recorded on #1

## Phase 1: Single-node MVP — blocked by #1

- Discovery of character and USB devices with stable IDs (#9)
- CDI spec generation and validation (#10)
- `node-generic-resources` management (#11)
- `spo devices`, `spo doctor` (#12)
- Exit: a Swarm service opens a USB device without privilege (#13)

## Phase 2: Cluster orchestration

- Node-local leases against same-node overlap (ADR 0002 L1, #15)
- Controller socket proxy (threat T10, #16)
- Certificate issuance and rotation (#17)
- Multi-manager failover, node loss, and partition tests

## Phase 3: GPU

- NVIDIA via `nvidia-ctk cdi generate`; GPU UUIDs as IDs
- AMD and Intel via DRM render nodes
- Health reporting

## Phase 4: Production readiness

- Metrics, dashboards, chaos tests, arm64 images, SBOM, signed releases (Release Please already owns versioning)

## Phase 5: Advanced

- MIG, shareable and capacity classes (ADR 0002 L2), hotplug rescheduling, UI
