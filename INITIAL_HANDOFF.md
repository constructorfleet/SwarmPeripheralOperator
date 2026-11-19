ndoff: Swarm Peripheral Operator (SPO)

**Working repository:** `swarm-peripheral-operator`  
**Working CLI:** `spo`  
**Language:** Go, unless prototyping establishes a compelling reason otherwise  
**License:** Apache-2.0  
**Target:** Linux, Docker Engine, Docker Swarm, OCI runtimes, CDI

## 1. Mission

Build a production-quality, open-source hardware orchestration system for Docker Swarm.

The system must allow Swarm services to discover, request, reserve, and access host hardware without relying on privileged application containers, hard-coded device paths, or device-specific hacks.

Hardware includes:

- NVIDIA, AMD, and Intel GPUs
- Coral USB and PCIe TPUs
- USB devices and serial adapters
- Video capture and V4L2 devices
- DRM render devices
- Sound and other Linux character devices
- Custom devices exposed through `/dev`
- Future FPGA and accelerator integrations

The solution must provide functionality comparable to Kubernetes device plugins and CDI-aware orchestration while preserving Docker Swarm's native lifecycle.

**Primary objective:** A user declares the hardware capabilities their service requires, and SPO handles discovery, allocation, placement eligibility, device injection, and lifecycle management.

## Mandatory Phase 0 Gate: Architecture Feasibility

**Phase 0 is a hard, blocking prerequisite for all implementation work.**

No Phase 1 or subsequent implementation may begin until Phase 0 has completed and its feasibility gate has been explicitly satisfied.

### Gate requirements

Phase 0 must demonstrate, using a real Docker Swarm environment:

1. A genuine Swarm task can receive hardware access through the proposed integration mechanism.
2. Device allocation identity can be securely propagated from the controller to the container runtime.
3. Device injection occurs before the application process starts.
4. The task remains under native Swarm lifecycle management.
5. The proposed approach does not require privileged application containers.
6. Device allocation can be revoked or safely reclaimed without creating conflicting assignments.
7. The approach works with documented, reproducible Docker Engine and OCI runtime versions.

### Required evidence

The gate cannot pass based on design documents, theoretical API compatibility, or untested assumptions.

Required artifacts:

- Working proof-of-concept implementation
- Automated integration tests
- Reproducible test environment
- Documented Docker/Swarm compatibility matrix
- Security and lifecycle analysis
- Architecture Decision Record identifying the selected integration mechanism
- Evidence that the tested mechanism preserves Swarm behavior

### Gate outcomes

**PASS:** All required capabilities are demonstrated. Record the evidence and authorize Phase 1.

**FAIL:** A fundamental limitation prevents a requirement from being satisfied. Stop implementation and document the blocker.

**CONDITIONAL:** A solution requires Docker Engine or SwarmKit modifications, or a change to the proposed architecture. Present the alternatives and trade-offs. Do not proceed until a revised architecture passes the gate.

### Enforcement

- Every Phase 1+ issue must depend on the Phase 0 gate issue.
- The gate issue must have explicit acceptance criteria and links to evidence.
- CI must run the feasibility integration suite on relevant changes.
- An architecture document alone cannot satisfy the gate.
- Agents must not bypass the gate, assume approval, or downgrade requirements to unblock implementation.
- Changes that invalidate the proven integration assumptions reopen the gate.
- A failed gate requires investigation or a revised approach, not speculative continuation.

**The purpose of Phase 0 is to establish that the proposed product can actually exist within Docker Swarm's architectural constraints. Everything else depends on that result.**

## 2. Research and feasibility gate

Before implementation, establish precisely what Docker Engine and Swarm currently support.

Study:

- Docker Engine device requests and CDI support
- SwarmKit service and task specifications
- Generic resources and node availability
- Docker daemon OCI runtime configuration
- containerd and runc integration points
- CDI specification and reference implementations
- NVIDIA Container Toolkit
- `leinardi/swarm-device-access`
- `allfro/device-mapping-manager`
- Kubernetes device plugin and Dynamic Resource Allocation architecture

**Do not assume that existing Docker CLI CDI support means Swarm supports arbitrary CDI devices.**

Create a compatibility matrix for currently supported Docker Engine releases and identify the exact API and lifecycle boundaries that prevent Swarm from expressing or honoring device requests.

Evaluate three integration strategies:

1. **Native Swarm integration:** contribute or maintain a patch to SwarmKit or Docker Engine that propagates device requests into task container creation.
2. **OCI runtime integration:** implement a narrowly scoped OCI runtime wrapper that resolves SPO device allocations and modifies the OCI specification before execution.
3. **Post-start compatibility:** use an opt-in device permission daemon similar to `swarm-device-access`, with explicit limitations.

Determine whether an OCI runtime wrapper receives sufficient service/task identity and allocation metadata to safely identify the correct devices. Verify where device mounts, permissions, and driver components must be injected in the container creation lifecycle.

The preferred design uses CDI and pre-start injection, but **feasibility must be proven before committing to that architecture**.

Deliver an architecture decision record and a working proof of concept that runs one GPU service and one USB-device service as genuine Swarm tasks.

Do not silently replace Swarm tasks with standalone Docker containers.

## 3. System architecture

Design five principal components.

### A. SPO Node Agent (`spo-agent`)

One agent runs on each participating Swarm node.

Responsibilities:

- Discover local hardware.
- Monitor device additions and removals.
- Read relevant udev, sysfs, PCI, and USB information.
- Detect device capabilities and health.
- Provide stable device identities.
- Publish inventory and availability.
- Generate or consume CDI device definitions.
- Enforce locally authorized allocations.
- Reconcile reservations with running tasks.
- Report metrics and diagnostic information.

Support Linux cgroup v1 and v2 where practical, but prioritize modern cgroup v2 hosts.

Avoid granting the agent unnecessary host privileges. Separate inventory-only and privileged operations where possible.

### B. SPO Controller (`spo-controller`)

A cluster-level control plane responsible for:

- Aggregating node device inventories.
- Resolving service device requirements.
- Matching devices against capabilities.
- Coordinating reservations.
- Reconciling Swarm service and task state.
- Detecting stale allocations.
- Handling node failures and device removal.
- Publishing placement eligibility.
- Managing allocation state and policy.

Use an explicit leader election or coordination mechanism compatible with Swarm.

Do not assume Swarm's internal Raft state can be safely extended by an external controller.

Maintain a durable allocation record with generation numbers and fencing tokens. Design for concurrent reconciliation, leader failover, and duplicated Docker events.

The controller must never allocate the same exclusive device to two concurrent workloads.

### C. SPO Device Plugins (`spo-plugin-*`)

Create a pluggable discovery and capability model.

Initial plugins:

| Plugin | Purpose |
|---|---|
| `spo-plugin-nvidia` | NVIDIA GPU discovery and CDI integration |
| `spo-plugin-drm` | DRM render and graphics devices |
| `spo-plugin-usb` | USB and serial hardware |
| `spo-plugin-coral` | Coral TPU discovery and capability matching |
| `spo-plugin-generic` | Administrator-defined device mappings |

Do not reimplement NVIDIA's driver discovery and CDI generator if the existing toolkit can provide authoritative device specifications.

Plugins should support:

- Stable device identities
- Device metadata and capabilities
- Health checks
- Resource sharing policies
- Device-specific CDI definitions
- Hotplug events
- Optional driver requirements

Provide a documented plugin interface with a compatibility/version contract.

### D. SPO Runtime Integration (`spo-runtime`)

Build or adapt an OCI runtime integration mechanism that:

1. Obtains the container's verified SPO task/allocation identity.
2. Resolves the device allocation.
3. Loads the corresponding CDI specification.
4. Applies the required OCI device entries.
5. Injects device mounts, environment variables, libraries, and supported CDI hooks.
6. Ensures appropriate kernel device permissions.
7. Delegates container execution to the underlying runtime.

Enforce strict allocation ownership. A workload must not be able to request arbitrary device IDs through untrusted environment variables or labels.

Do not blindly make every device available to every SPO-enabled workload.

Where OCI integration is impossible for a given Docker version, provide a clearly documented alternative rather than silently falling back to a less secure approach.

### E. SPO CLI (`spo`)

Provide an administrator and developer interface.

Target commands:

```bash
spo cluster status
spo nodes
spo devices list
spo devices inspect <device-id>
spo devices health
spo allocations list
spo allocations inspect <allocation-id>
spo allocations release <allocation-id>
spo services validate <stack-file>
spo services plan <stack-file>
spo doctor
```

`spo doctor` should explain common problems, including:

- Missing kernel modules
- Missing driver libraries
- CDI specification errors
- Device nodes not accessible
- Unsupported runtime configuration
- Missing node permissions
- Allocation conflicts
- Swarm placement incompatibility

## 4. Declarative workload configuration

The user experience should feel like native Docker Compose.

Explore a Compose extension such as:

```yaml
services:
  inference:
    image: example/inference:latest

    x-spo:
      devices:
        - class: gpu
          vendor: nvidia
          count: 1
          capabilities:
            compute: true
          allocation: exclusive

    deploy:
      replicas: 1
```

The exact schema is provisional.

The implementation must document whether this extension is consumed through a preprocessing CLI, generated Swarm service metadata, or native integration.

Docker Stack may ignore extension fields. SPO must not assume the Docker daemon consumes them.

Desired deployment workflow:

```bash
spo stack validate -c stack.yaml
spo stack plan -c stack.yaml
spo stack deploy -c stack.yaml inference
```

Preserve standard Docker Stack semantics wherever possible.

SPO-generated service labels, reservations, and placement rules must be visible and inspectable.

Normal Swarm operations, including updates, rollback, removal, scaling, and restart, must continue to function.

## 5. Scheduling and allocation model

Define resource classes:

- `exclusive`
- `shareable`
- `partitioned`
- `capacity-based`

Exclusive hardware must have a single active allocation.

Shareable hardware requires explicit vendor/plugin support. Do not assume that simultaneous access to a device node implies safe sharing.

Partitioned devices may include NVIDIA MIG instances or other physically or logically partitioned accelerators.

Capacity-based resources may support requests such as minimum memory, compute capability, or supported features.

Use device-level identity rather than treating all GPUs or USB devices on a node as interchangeable.

### Critical constraint

Swarm generic resources provide count-based scheduling, but do not automatically guarantee an individual device identity or binding.

The controller must coordinate scheduling eligibility, reservations, and actual runtime injection.

Design a reservation protocol that avoids oversubscription and stale-device assignment, even during concurrent service updates.

If atomic scheduling cannot be achieved without an upstream Docker modification, document that limitation and implement a bounded, observable reconciliation protocol.

## 6. Device inventory and identity

Each discovered device should have a structured record.

Example:

```yaml
id: pci-0000-01-00-0
node: swarm05

class: gpu
vendor: nvidia

capabilities:
  compute: true
  graphics: true
  encoder: true

allocation:
  mode: exclusive
  state: available

health:
  status: healthy
```

Use stable hardware identifiers where available, including GPU UUIDs, PCI addresses, USB serial numbers, and topology.

Do not assume `/dev/nvidia0`, `/dev/video0`, or `/dev/ttyUSB0` remains stable across reboots.

Handle devices without globally unique serial numbers by using an explicit identity strategy and documenting its limitations.

## 7. Failure handling

The system must address:

- Node reboot
- Docker daemon restart
- Controller restart
- Agent restart
- Device unplug/replug
- GPU driver reset
- Device health degradation
- Swarm task replacement
- Rolling updates
- Allocation timeout
- Network partition
- Duplicate Docker events
- Stale task metadata
- Controller leadership changes
- Incomplete CDI definitions

Failures should trigger deterministic reconciliation.

Avoid restart loops caused by a missing hardware device.

The controller must distinguish between:

- Hardware unavailable
- Hardware unhealthy
- Hardware allocated elsewhere
- Runtime injection failure
- Swarm placement failure
- Device authorization failure

A device disappearing must not automatically result in its stale allocation being granted to another task that could conflict with the original workload.

## 8. Security model

Treat this as privileged host infrastructure.

Security requirements:

- Least-privilege design.
- No Docker socket access for normal application workloads.
- Explicit allocation authorization.
- Device allowlists.
- CDI specification validation.
- Safe handling of symlinks and device paths.
- Protection against path traversal and time-of-check/time-of-use races.
- Host driver/library injection restrictions.
- No untrusted arbitrary OCI hooks.
- Auditable device assignment.
- Protection against spoofed task metadata.
- Secure agent/controller communication.
- Secrets must never enter device metadata or logs.

Define the trust boundary between the controller, node agent, OCI runtime, device plugins, and service owners.

Do not claim that cgroup device filters alone isolate a GPU's memory, execution context, or firmware.

Include a threat model before implementing privileged runtime functionality.

## 9. Observability

Expose Prometheus-compatible metrics.

Examples:

```text
spo_devices_total
spo_devices_available
spo_devices_allocated
spo_device_health
spo_allocations_active
spo_allocation_conflicts_total
spo_reconciliation_duration_seconds
spo_runtime_injection_failures_total
spo_agent_last_seen_timestamp_seconds
```

Provide structured logs with correlation fields for:

- Node
- Device
- Service
- Task
- Allocation
- Reconciliation operation

Add optional OpenTelemetry tracing for the controller and allocation lifecycle.

Provide an example Grafana dashboard showing device inventory, allocation, health, and service placement.

## 10. Repository structure

Suggested starting layout:

```text
swarm-peripheral-operator/
├── cmd/
│   ├── spo/
│   ├── spo-agent/
│   ├── spo-controller/
│   └── spo-runtime/
├── internal/
│   ├── allocation/
│   ├── controller/
│   ├── discovery/
│   ├── docker/
│   ├── runtime/
│   └── security/
├── pkg/
│   ├── api/
│   ├── cdi/
│   ├── devices/
│   └── plugins/
├── plugins/
│   ├── nvidia/
│   ├── drm/
│   ├── usb/
│   ├── coral/
│   └── generic/
├── deployments/
│   ├── swarm/
│   └── systemd/
├── examples/
│   ├── nvidia/
│   ├── coral/
│   ├── usb-serial/
│   └── mixed-hardware/
├── docs/
│   ├── architecture/
│   ├── adr/
│   ├── security/
│   ├── compatibility/
│   └── operations/
├── tests/
│   ├── integration/
│   ├── e2e/
│   └── chaos/
├── AGENTS.md
├── CONTRIBUTING.md
├── SECURITY.md
└── README.md
```

Prefer established libraries and interfaces over custom implementations.

## 11. Development and delivery standards

Use:

- Test-driven development
- Conventional Commits
- Semantic versioning
- Automated releases
- CI/CD
- Go formatting and linting
- Dependency and vulnerability scanning
- SBOM generation
- Signed release artifacts where feasible
- Multi-architecture images (`amd64`, `arm64`)
- Integration tests using Docker-in-Docker or dedicated test VMs
- Hardware-in-the-loop tests for actual GPU/USB behavior

All implementation work should happen in isolated Git worktrees.

Use the repository's issue tracker to track bounded work items with acceptance criteria.

No feature is complete without relevant automated tests, documentation, and verified behavior.

## 12. Implementation phases

### Phase 0: Feasibility and compatibility

Deliver:

- Existing-solutions comparison
- Docker/Swarm compatibility matrix
- Architectural decision records
- OCI integration proof of concept
- Native Swarm task preservation demonstration
- Explicit documentation of unsupported capabilities

**Exit gate:** Demonstrate that the intended runtime injection mechanism actually receives trusted allocation identity and can safely affect device visibility before the workload starts.

If this is impossible without patching Docker, halt runtime development and present the native integration versus compatibility-mode options.

### Phase 1: Single-node MVP

Support:

- Generic character devices
- USB devices
- CDI specification loading
- Node-local discovery
- One service requesting one device
- Verified pre-start device injection
- Basic CLI and diagnostics

**Exit gate:** A genuine Swarm service accesses a specified USB device without being privileged and without manually bind-mounting its `/dev` path into the application service.

### Phase 2: Cluster orchestration

Implement:

- Cluster inventory
- Allocation leases and fencing
- Device-aware placement
- Controller reconciliation
- Node failure handling
- Multi-node integration tests

**Exit gate:** Multiple concurrent services requesting exclusive hardware receive nonconflicting allocations and recover safely from node or controller failures.

### Phase 3: GPU integration

Implement:

- Existing NVIDIA CDI integration
- GPU identity and capabilities
- GPU selection and allocation
- Optional AMD/Intel integrations
- GPU health reporting

**Exit gate:** A workload requiring NVIDIA GPU access is scheduled onto an eligible node, receives the selected GPU, and successfully executes a GPU computation.

### Phase 4: Production readiness

Implement:

- Hardened security policies
- Observability dashboards
- Rolling upgrade support
- Failure injection and chaos tests
- Installation and troubleshooting tooling
- ARM64 compatibility
- Release automation

**Exit gate:** Sustained automated testing demonstrates safe allocation behavior across restarts, updates, failures, and competing workloads.

### Phase 5: Advanced hardware orchestration

Explore:

- NVIDIA MIG-aware scheduling
- Device pools
- Vendor-specific health diagnostics
- Device reservation priorities
- Capacity-based GPU allocation
- Hotplug-driven rescheduling
- Web UI for device inventory and allocations

Do not begin these features before the core scheduling and runtime integration are reliable.

## 13. Definition of done

The first production-capable release must demonstrate:

1. A Swarm service can declare a generic hardware requirement.
2. SPO discovers eligible devices across nodes.
3. A service is placed onto an eligible node.
4. A specific device is reserved without allocation conflict.
5. Device injection occurs before application execution.
6. The application runs without privileged mode.
7. Native Swarm lifecycle management remains intact.
8. A task restart or reschedule does not leak allocations.
9. Device failures produce meaningful diagnostics.
10. Operators can inspect inventory, allocations, and health.
11. Controller failure does not lead to conflicting allocations.
12. Tests prove that unauthorized services cannot obtain device access through SPO.

## 14. Immediate execution instructions

Start by researching the available Docker Engine, SwarmKit, containerd, OCI, and CDI integration interfaces.

Produce:

- `docs/architecture/overview.md`
- `docs/architecture/runtime-integration.md`
- `docs/architecture/allocation-protocol.md`
- `docs/compatibility/docker-swarm.md`
- `docs/security/threat-model.md`
- `docs/adr/0001-runtime-integration.md`
- `docs/adr/0002-allocation-coordination.md`
- `docs/roadmap.md`
- `AGENTS.md`

Create tracked implementation issues for Phase 0 and Phase 1, with dependencies and measurable acceptance criteria.

Implement the smallest viable proof of concept before adding additional device plugins, scheduling strategies, or a web interface.

**Non-negotiable:** Do not produce a convincing-looking architecture that cannot actually integrate with Docker Swarm. Verify the runtime and scheduling boundaries against real Docker Engine behavior before building the system around them.

